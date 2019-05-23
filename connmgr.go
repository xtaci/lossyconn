package lossyconn

import "sync"

var packetConns *ConnectionManager

func init() {
	packetConns = NewConnectionManager()
}

type ConnectionManager struct {
	conns map[string]*LossyPacketConn
	mu    sync.Mutex
}

func NewConnectionManager() *ConnectionManager {
	mgr := new(ConnectionManager)
	mgr.conns = make(map[string]*LossyPacketConn)
	return mgr
}

// return a connection from the pool
func (mgr *ConnectionManager) Get(addr string) *LossyPacketConn {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.conns[addr]
}

// add a connection to the pool
func (mgr *ConnectionManager) Set(conn *LossyPacketConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.conns[conn.LocalAddr().String()] = conn
}

// delete a connection from the pool
func (mgr *ConnectionManager) Delete(conn *LossyPacketConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	delete(mgr.conns, conn.LocalAddr().String())
}
