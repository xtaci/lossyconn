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

// TimedSender delivers packets to connections at their scheduled times.
// Call Close when the sender is no longer needed.
type TimedSender struct {
	entries    []entry
	chNotify   chan struct{}
	chStop     chan struct{}
	chDone     chan struct{}
	maxPending int
	closed     bool
	mu         sync.Mutex
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

// NewTimedSender creates an unbounded sender. Call Close when finished.
func NewTimedSender() *TimedSender {
	return newTimedSender(0)
}

func newTimedSender(maxPending int) *TimedSender {
	dw := &TimedSender{
		chNotify:   make(chan struct{}, 1),
		chStop:     make(chan struct{}),
		chDone:     make(chan struct{}),
		maxPending: maxPending,
	}
	go dw.sendLoop()
	return dw
}

func (h *TimedSender) notify() {
	select {
	case h.chNotify <- struct{}{}:
	default:
	}
}

// Send schedules packet for delivery after delay. Calls made after Close are ignored.
func (h *TimedSender) Send(conn *LossyConn, packet Packet, delay time.Duration) {
	h.trySend(conn, packet, delay)
}

func (h *TimedSender) trySend(conn *LossyConn, packet Packet, delay time.Duration) bool {
	return h.trySendBatch(conn, []Packet{packet}, delay)
}

func (h *TimedSender) trySendBatch(conn *LossyConn, packets []Packet, delay time.Duration) bool {
	h.mu.Lock()
	if h.closed || (h.maxPending > 0 && h.Len()+len(packets) > h.maxPending) {
		h.mu.Unlock()
		return false
	}
	ts := time.Now().Add(delay)
	for _, packet := range packets {
		heap.Push(h, entry{ts, packet, conn})
	}
	h.mu.Unlock()
	if len(packets) > 0 {
		h.notify()
	}
	return true
}

func (h *TimedSender) pending() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Len()
}

// Close stops the sender and discards packets that have not yet been delivered.
// It is safe to call Close more than once.
func (h *TimedSender) Close() error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.entries = nil
		close(h.chStop)
	}
	h.mu.Unlock()
	<-h.chDone
	return nil
}

func (h *TimedSender) sendLoop() {
	defer close(h.chDone)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return
		}
		hasEntries := h.Len() > 0
		if h.Len() > 0 && !time.Now().Before(h.entries[0].ts) {
			entry := heap.Pop(h).(entry)
			h.mu.Unlock()
			entry.conn.receivePacket(entry.packet)
			continue
		}
		var wait time.Duration
		if h.Len() > 0 {
			wait = time.Until(h.entries[0].ts)
		}
		h.mu.Unlock()

		if wait <= 0 {
			if hasEntries {
				continue
			}
			select {
			case <-h.chNotify:
				continue
			case <-h.chStop:
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-h.chNotify:
		case <-h.chStop:
			return
		}
	}
}
