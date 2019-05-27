package lossyconn

import "sync"

var defaultConnectionManager *ConnectionManager

func init() {
	defaultConnectionManager = NewConnectionManager()
}

// ConnectionManager contains all lossy connections
type ConnectionManager struct {
	conns map[string]*LossyConn
	mu    sync.Mutex
}

// NewConnectionManager create a connection manager for all LossyConn
func NewConnectionManager() *ConnectionManager {
	mgr := new(ConnectionManager)
	mgr.conns = make(map[string]*LossyConn)
	return mgr
}

// return a connection from the pool
func (mgr *ConnectionManager) Get(addr string) *LossyConn {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.conns[addr]
}

// add a connection to the pool
func (mgr *ConnectionManager) Set(conn *LossyConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.conns[conn.LocalAddr().String()] = conn
}

// delete a connection from the pool
func (mgr *ConnectionManager) Delete(conn *LossyConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	delete(mgr.conns, conn.LocalAddr().String())
}
