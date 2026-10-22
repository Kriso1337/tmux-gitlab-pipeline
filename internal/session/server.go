package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

type Handler func(context.Context, Request) (json.RawMessage, error)
type ServerOptions struct {
	Version     string
	Cache       CacheLimits
	Workers     int
	Connections int
	Queue       int
	Targets     int
	Refresh     time.Duration
	Warm        time.Duration
	TTL         time.Duration
	Sweep       time.Duration
	Alive       func(context.Context) bool
	Notify      func(context.Context, Request, json.RawMessage, json.RawMessage) error
}

func DefaultServerOptions(version string) ServerOptions {
	return ServerOptions{Version: version, Cache: CacheLimits{Bytes: 64 << 20, Entries: 256, ValueBytes: 8 << 20}, Workers: 4, Connections: 16, Queue: 32, Targets: 16, Refresh: 5 * time.Second, Warm: time.Minute, TTL: 30 * time.Minute, Sweep: time.Second}
}

type flight struct {
	request Request
	key     string
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}
type watch struct {
	request              Request
	until, next, touched time.Time
}
type ServerStats struct {
	Cache            CacheStats
	Targets, Flights int
}
type Server struct {
	options ServerOptions
	handler Handler
	cache   *Cache
	mu      sync.Mutex
	flights map[string]*flight
	watches map[string]watch
	queue   chan *flight
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewServer(options ServerOptions, handler Handler) (*Server, error) {
	if handler == nil || options.Workers < 1 || options.Workers > 8 || options.Connections < 1 || options.Connections > 32 || options.Queue < options.Workers || options.Queue > 128 || options.Targets < 1 || options.Targets > 32 || options.Refresh <= 0 || options.Warm <= 0 || options.TTL <= 0 || options.Sweep <= 0 {
		return nil, fmt.Errorf("invalid session server options")
	}
	cache, err := NewCache(options.Cache)
	if err != nil {
		return nil, err
	}
	return &Server{options: options, handler: handler, cache: cache, flights: make(map[string]*flight), watches: make(map[string]watch), queue: make(chan *flight, options.Queue)}, nil
}
func closeSocket(closer io.Closer) {
	if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Error("close session socket", "error", err)
	}
}
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	for range s.options.Workers {
		s.wg.Add(1)
		go s.worker()
	}
	s.wg.Add(1)
	go s.sweep()
	stop := context.AfterFunc(s.ctx, func() { closeSocket(listener) })
	defer stop()
	defer func() {
		s.cancel()
		closeSocket(listener)
		s.wg.Wait()
		for {
			select {
			case item := <-s.queue:
				item.cancel()
				close(item.done)
			default:
				s.cache.Clear()
				s.mu.Lock()
				clear(s.watches)
				clear(s.flights)
				s.mu.Unlock()
				return
			}
		}
	}()
	connections := make(chan struct{}, s.options.Connections)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept session client: %w", err)
		}
		select {
		case connections <- struct{}{}:
			s.wg.Add(1)
			go func() { defer s.wg.Done(); defer func() { <-connections }(); s.connection(conn) }()
		default:
			closeSocket(conn)
		}
	}
}
func (s *Server) connection(conn net.Conn) {
	defer closeSocket(conn)
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		slog.Error("set session read deadline", "error", err)
		return
	}
	stop := context.AfterFunc(s.ctx, func() { closeSocket(conn) })
	defer stop()
	var request Request
	input := &io.LimitedReader{R: conn, N: MaxRequestBytes + 1}
	decoder := json.NewDecoder(input)
	if err := decoder.Decode(&request); err != nil {
		return
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || input.N <= 0 {
		return
	}
	if err := conn.SetDeadline(time.Now().Add(95 * time.Second)); err != nil {
		slog.Error("set session response deadline", "error", err)
		return
	}
	response := Response{Protocol: Protocol, Version: s.options.Version}
	if err := request.validate(); err != nil {
		response.Error = err.Error()
	} else {
		switch request.Operation {
		case PingOperation:
		case ShutdownOperation:
			defer s.cancel()
		case StatsOperation:
			s.mu.Lock()
			stats := ServerStats{Cache: s.cache.Stats(), Targets: len(s.watches), Flights: len(s.flights)}
			s.mu.Unlock()
			data, err := json.Marshal(stats)
			response.Data, response.Error = data, errorText(err)
		case ClearOperation, InvalidateOperation:
			s.invalidate(request)
		default:
			response = s.read(request)
		}
	}
	if err := json.NewEncoder(conn).Encode(response); err != nil && s.ctx.Err() == nil {
		slog.Debug("session client disconnected", "error", err)
	}
}
func (s *Server) read(request Request) Response {
	now := time.Now()
	key := request.key()
	s.touch(request, key, now)
	record, found := s.cache.Get(key, now)
	if found && !request.Force {
		refreshing := false
		if !now.Before(record.FreshUntil) {
			if _, err := s.enqueue(request, key); err != nil {
				record.Error = errorText(err)
			} else {
				refreshing = true
			}
		}
		return Response{Protocol: Protocol, Version: s.options.Version, Data: record.Data, Error: record.Error, Cached: true, Refreshing: refreshing}
	}
	item, err := s.enqueue(request, key)
	if err != nil {
		return Response{Protocol: Protocol, Version: s.options.Version, Error: errorText(err)}
	}
	if request.Quick {
		return Response{Protocol: Protocol, Version: s.options.Version, Refreshing: true}
	}
	select {
	case <-s.ctx.Done():
		return Response{Protocol: Protocol, Version: s.options.Version, Error: s.ctx.Err().Error()}
	case <-item.done:
		record, found = s.cache.Get(key, time.Now())
		if !found {
			return Response{Protocol: Protocol, Version: s.options.Version, Error: "session cache entry was invalidated"}
		}
		return Response{Protocol: Protocol, Version: s.options.Version, Data: record.Data, Error: record.Error}
	}
}
func (s *Server) enqueue(request Request, key string) (*flight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueueLocked(request, key)
}
func (s *Server) enqueueLocked(request Request, key string) (*flight, error) {
	if existing := s.flights[key]; existing != nil && existing.ctx.Err() == nil {
		return existing, nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
	item := &flight{request: request, key: key, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	select {
	case s.queue <- item:
		s.flights[key] = item
		return item, nil
	default:
		cancel()
		return nil, fmt.Errorf("session refresh queue is full")
	}
}
func (s *Server) touch(request Request, key string, now time.Time) {
	if request.Operation != SnapshotOperation {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.watches[key]; !found && len(s.watches) >= s.options.Targets {
		oldestKey := ""
		var oldest time.Time
		for k, item := range s.watches {
			if oldestKey == "" || item.touched.Before(oldest) {
				oldestKey, oldest = k, item.touched
			}
		}
		delete(s.watches, oldestKey)
	}
	item := s.watches[key]
	request.Force = false
	request.Quick = false
	item.request, item.until, item.touched = request, now.Add(s.options.Warm), now
	if item.next.IsZero() {
		item.next = now.Add(s.options.Refresh)
	}
	s.watches[key] = item
}
func (s *Server) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case item := <-s.queue:
			s.fetch(item)
		}
	}
}
func (s *Server) fetch(item *flight) {
	defer item.cancel()
	var data json.RawMessage
	err := item.ctx.Err()
	if err == nil {
		data, err = s.handler(item.ctx, item.request)
	}
	limit := s.options.Cache.ValueBytes
	if item.request.Operation == SnapshotOperation {
		limit = min(limit, 4<<20)
	}
	if err == nil && (len(data) > limit || !json.Valid(data)) {
		err = fmt.Errorf("CI response exceeds session cache limits or is invalid JSON")
		data = nil
	}
	now := time.Now()
	previous, exists := s.cache.Get(item.key, now)
	record := Record{Data: data, WrittenAt: now, FreshUntil: now.Add(s.options.Refresh), ExpiresAt: now.Add(s.options.TTL)}
	if item.request.Operation == TraceOperation {
		record.FreshUntil = now.Add(2 * time.Second)
		if item.request.Job.Status.Finished() {
			record.FreshUntil = record.ExpiresAt
		}
	}
	if err != nil {
		record = previous
		record.Error = errorText(err)
		record.Failures++
		record.FreshUntil = now.Add(min(time.Minute, s.options.Refresh*time.Duration(1<<min(record.Failures, 4))))
		if !exists {
			record.ExpiresAt = record.FreshUntil.Add(s.options.Refresh)
		}
	}
	s.mu.Lock()
	current := s.flights[item.key] == item && s.ctx.Err() == nil
	if current {
		if putErr := s.cache.Put(item.key, record); putErr != nil {
			err = errors.Join(err, putErr)
			record = Record{Error: errorText(err), FreshUntil: now.Add(s.options.Refresh), ExpiresAt: now.Add(s.options.Refresh)}
			if putErr = s.cache.Put(item.key, record); putErr != nil {
				slog.Error("store session cache failure", "error", putErr)
			}
		}
		if watched, ok := s.watches[item.key]; ok {
			watched.next = record.FreshUntil
			s.watches[item.key] = watched
		}
	}
	if s.flights[item.key] == item {
		delete(s.flights, item.key)
	}
	close(item.done)
	s.mu.Unlock()
	if current && err == nil && exists && s.options.Notify != nil && item.request.Operation == SnapshotOperation {
		ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
		defer cancel()
		if notifyErr := s.options.Notify(ctx, item.request, previous.Data, data); notifyErr != nil {
			slog.Error("notify CI transition", "error", notifyErr)
		}
	}
}
func (s *Server) invalidate(request Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := request.Target.Prefix()
	for key, item := range s.flights {
		if request.Operation == ClearOperation || strings.HasPrefix(key, prefix) {
			item.cancel()
			delete(s.flights, key)
		}
	}
	for key := range s.watches {
		if request.Operation == ClearOperation || strings.HasPrefix(key, prefix) {
			delete(s.watches, key)
		}
	}
	if request.Operation == ClearOperation {
		s.cache.Clear()
	} else {
		s.cache.Invalidate(prefix)
	}
}
func (s *Server) sweep() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.options.Sweep)
	defer ticker.Stop()
	nextAlive := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.cache.Sweep(now)
			s.mu.Lock()
			for key, item := range s.watches {
				if !now.Before(item.until) {
					delete(s.watches, key)
					continue
				}
				if !now.Before(item.next) {
					if _, err := s.enqueueLocked(item.request, key); err != nil {
						slog.Debug("defer session refresh", "error", err)
					}
					item.next = now.Add(s.options.Refresh)
					s.watches[key] = item
				}
			}
			s.mu.Unlock()
			if s.options.Alive != nil && !now.Before(nextAlive) {
				ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
				alive := s.options.Alive(ctx)
				cancel()
				if !alive {
					s.cancel()
					return
				}
				nextAlive = now.Add(5 * time.Second)
			}
		}
	}
}
func errorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 4096 {
		text = text[:4096]
	}
	return text
}
