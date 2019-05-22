package lossyconn

import "sync"

var packetConns *PacketConns

func init() {
	packetConns = NewPacketConns()
}

type PacketConns struct {
	conns map[string]*LossyPacketConn
	mu    sync.Mutex
}

func NewPacketConns() *PacketConns {
	mgr := new(PacketConns)
	mgr.conns = make(map[string]*LossyPacketConn)
	return mgr
}

func (mgr *PacketConns) Get(addr string) *LossyPacketConn {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.conns[addr]
}

func (mgr *PacketConns) Set(conn *LossyPacketConn) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.conns[conn.LocalAddr().String()] = conn
}
