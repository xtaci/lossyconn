package lossyconn

import (
	"container/heap"
	"sync"
	"time"
)

type entry struct {
	ts     time.Time
	packet Packet
	conn   *LossyPacketConn
}

type DelayedWriter struct {
	entries  []entry
	chNotify chan struct{}
	mu       sync.Mutex
}

func (h *DelayedWriter) Len() int           { return len(h.entries) }
func (h *DelayedWriter) Less(i, j int) bool { return h.entries[i].ts.Before(h.entries[j].ts) }
func (h *DelayedWriter) Swap(i, j int)      { h.entries[i], h.entries[j] = h.entries[j], h.entries[i] }
func (h *DelayedWriter) Push(x interface{}) { h.entries = append(h.entries, x.(entry)) }

func (h *DelayedWriter) Pop() interface{} {
	n := len(h.entries)
	x := h.entries[n-1]
	h.entries = h.entries[0 : n-1]
	return x
}

func NewDelayedWriter() *DelayedWriter {
	dw := new(DelayedWriter)
	dw.chNotify = make(chan struct{}, 1)
	go dw.sendLoop()
	return dw
}

func (h *DelayedWriter) notify() {
	select {
	case h.chNotify <- struct{}{}:
	default:
	}
}

// Send with a delay
func (h *DelayedWriter) Send(conn *LossyPacketConn, packet Packet, delay time.Duration) {
	h.mu.Lock()
	heap.Push(h, entry{time.Now().Add(delay), packet, conn})
	h.mu.Unlock()
	h.notify()
}

func (h *DelayedWriter) sendLoop() {
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
		case <-h.chNotify:
		}

		h.mu.Lock()
		hlen := h.Len()
		for i := 0; i < hlen; i++ {
			entry := &h.entries[0]
			if !time.Now().Before(entry.ts) {
				entry.conn.receivePacket(entry.packet)
				heap.Pop(h)
			} else {
				break
			}
		}

		if h.Len() > 0 {
			timer.Reset(h.entries[0].ts.Sub(time.Now()))
		}
		h.mu.Unlock()
	}
}
