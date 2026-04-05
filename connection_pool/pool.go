package connectionpool

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// QueryableConn is the caller-facing interface.
// Callers only see what they need to use a connection.
// Pool internals are hidden.
type QueryableConn interface {
	Query(query string, args ...any) (any, error)
	Exec(query string, args ...any) error
}

// poolConn is the pool-internal interface.
// Only the pool calls these methods for lifecycle management.
type poolConn interface {
	IsClosed() bool
	Close() error
	Ping() error
}

// Connection is the full interface that connection implementors must satisfy.
// It composes both caller-facing and pool-internal concerns.
// CreateConnectionFn must return this.
type Connection interface {
	QueryableConn
	poolConn
}

// CreateConnectionFn is provided by the caller to create datasource-specific connections.
// The caller implements Connection for their datasource (PG, Redis, MySQL etc.)
type CreateConnectionFn func() (Connection, error)

type ConnectionPoolConfig struct {
	CreateConnectionFn CreateConnectionFn
	MaxConnections     int
	MinConnections     int
	GetConnTimeout     time.Duration
	PutConnTimeout     time.Duration
	IdleTimeout        time.Duration
}

type connectionInfo struct {
	c         Connection
	createdAt time.Time
}

type connectionPool struct {
	connInfoChan chan connectionInfo
	cfg          *ConnectionPoolConfig
	totalNumConn atomic.Int64
}

func NewConnectionPool(ctx context.Context, c *ConnectionPoolConfig) (*connectionPool, error) {
	cpool := &connectionPool{
		connInfoChan: make(chan connectionInfo, c.MaxConnections),
		cfg:          c,
	}
	for i := 0; i < c.MinConnections; i++ {
		conn, err := c.CreateConnectionFn()
		if err != nil {
			return nil, fmt.Errorf("error initialising connection: %w", err)
		}
		cpool.totalNumConn.Add(1)
		cpool.connInfoChan <- connectionInfo{
			c:         conn,
			createdAt: time.Now(),
		}
	}
	return cpool, nil
}

// Get returns a QueryableConn — caller cannot see pool internals like Close or IsClosed.
// Caller must defer the returned cleanup func to return the connection to the pool.
//
// Usage:
//
//	conn, cleanup, err := pool.Get(ctx)
//	if err != nil { ... }
//	defer cleanup()
//	conn.Query(...)
func (c *connectionPool) Get(ctx context.Context) (QueryableConn, func(), error) {
	conn, err := c.get(ctx)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		if r := recover(); r != nil {
			// Panic path — connection is unusable, free the slot
			if !conn.IsClosed() {
				_ = conn.Close()
			}
			c.totalNumConn.Add(-1)
			// Re-panic so caller's stack unwinds normally
			panic(r)
		}
		// Normal path — return connection to pool
		_ = c.put(ctx, conn)
	}

	return conn, cleanup, nil
}

func (c *connectionPool) get(ctx context.Context) (Connection, error) {
	dur := time.After(c.cfg.GetConnTimeout)
	for {
		select {
		case cx := <-c.connInfoChan:
			if c.isStale(cx) {
				c.evict(cx.c)
				continue
			}
			return cx.c, nil

		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled")

		case <-dur:
			return nil, fmt.Errorf("timed out waiting for connection")

		default:
			if c.totalNumConn.Load() < int64(c.cfg.MaxConnections) {
				// Optimistically reserve a slot
				c.totalNumConn.Add(1)
				conn, err := c.cfg.CreateConnectionFn()
				if err != nil {
					c.totalNumConn.Add(-1)
					return nil, fmt.Errorf("error creating connection: %w", err)
				}
				return conn, nil
			}

			// Pool exhausted — block until a connection is returned or timeout
			select {
			case cx := <-c.connInfoChan:
				if c.isStale(cx) {
					c.evict(cx.c)
					continue
				}
				return cx.c, nil
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled")
			case <-dur:
				return nil, fmt.Errorf("timed out waiting for connection")
			}
		}
	}
}

// put is unexported — callers use the cleanup func returned by Get.
func (c *connectionPool) put(ctx context.Context, conn Connection) error {
	if conn.IsClosed() {
		c.totalNumConn.Add(-1)
		return fmt.Errorf("cannot put closed connection in pool")
	}

	connInfo := connectionInfo{
		c:         conn,
		createdAt: time.Now(), // reset idle clock on return
	}

	timeout := time.After(c.cfg.PutConnTimeout)
	select {
	case c.connInfoChan <- connInfo:
		return nil
	case <-timeout:
		// Pool is full — discard connection and free slot
		c.totalNumConn.Add(-1)
		_ = conn.Close()
		return fmt.Errorf("timed out returning connection to pool")
	case <-ctx.Done():
		c.totalNumConn.Add(-1)
		_ = conn.Close()
		return fmt.Errorf("context cancelled")
	}
}

func (c *connectionPool) isStale(cx connectionInfo) bool {
	return time.Since(cx.createdAt) > c.cfg.IdleTimeout || cx.c.IsClosed()
}

func (c *connectionPool) evict(conn Connection) {
	c.totalNumConn.Add(-1)
	if !conn.IsClosed() {
		_ = conn.Close()
	}
}
