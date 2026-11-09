// MIT License
//
// Copyright (c) 2019 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

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
