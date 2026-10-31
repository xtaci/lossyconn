package lossyconn

import "sync"

var defaultConnectionManager *ConnectionManager

func init() {
	defaultConnectionManager = NewConnectionManager()
}

// ConnectionManager indexes simulated connections by local address.
type ConnectionManager struct {
	conns map[string]*LossyConn
	mu    sync.RWMutex
}

// NewConnectionManager creates an empty connection manager.
func NewConnectionManager() *ConnectionManager {
	mgr := new(ConnectionManager)
	mgr.conns = make(map[string]*LossyConn)
	return mgr
}

// Get returns the connection registered for addr, or nil if none exists.
func (mgr *ConnectionManager) Get(addr string) *LossyConn {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.conns[addr]
}

// Set registers conn by its local address.
func (mgr *ConnectionManager) Set(conn *LossyConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.conns[conn.LocalAddr().String()] = conn
}

// Delete removes conn from the manager.
func (mgr *ConnectionManager) Delete(conn *LossyConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	delete(mgr.conns, conn.LocalAddr().String())
}
