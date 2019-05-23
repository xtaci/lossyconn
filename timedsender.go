package lossyconn

import (
	"container/heap"
	"sync"
	"time"
)

type entry struct {
	ts     time.Time
	packet Packet
	conn   *LossyConn
}

// TimedSender sends Packet to a connection at given time
type TimedSender struct {
	entries  []entry
	chNotify chan struct{}
	mu       sync.Mutex
}

func (h *TimedSender) Len() int           { return len(h.entries) }
func (h *TimedSender) Less(i, j int) bool { return h.entries[i].ts.Before(h.entries[j].ts) }
func (h *TimedSender) Swap(i, j int)      { h.entries[i], h.entries[j] = h.entries[j], h.entries[i] }
func (h *TimedSender) Push(x interface{}) { h.entries = append(h.entries, x.(entry)) }

func (h *TimedSender) Pop() interface{} {
	n := len(h.entries)
	x := h.entries[n-1]
	h.entries = h.entries[0 : n-1]
	return x
}

func NewTimedSender() *TimedSender {
	dw := new(TimedSender)
	dw.chNotify = make(chan struct{}, 1)
	go dw.sendLoop()
	return dw
}

func (h *TimedSender) notify() {
	select {
	case h.chNotify <- struct{}{}:
	default:
	}
}

// Send with a delay
func (h *TimedSender) Send(conn *LossyConn, packet Packet, delay time.Duration) {
	h.mu.Lock()
	heap.Push(h, entry{time.Now().Add(delay), packet, conn})
	h.mu.Unlock()
	h.notify()
}

func (h *TimedSender) sendLoop() {
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
		case <-h.chNotify:
		}

		h.mu.Lock()
		for h.Len() > 0 {
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
