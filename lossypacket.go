// Package lossyconn is a lossy connection simulator for Golang.
//
// lossyconn provides packet oriented lossy connection for testing purpose
package lossyconn

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Address defines a simulated lossy conn address
type Address struct {
	str string
}

// NewAddress creates a simulated address by filling with random numbers
func NewAddress() *Address {
	addr := new(Address)
	fakeaddr := make([]byte, 16)
	io.ReadFull(crand.Reader, fakeaddr)
	addr.str = fmt.Sprintf("%s", fakeaddr)
	return addr
}

func (addr *Address) Network() string {
	return "lossy"
}
func (addr *Address) String() string {
	return addr.str
}

// Packet defines a data to be sent at some specific time
type Packet struct {
	addr    net.Addr
	payload []byte
	ts      time.Time
}

// Config controls link simulation behavior.
type Config struct {
	// Base one-way delay.
	Delay time.Duration
	// Normal-distribution delay deviation in milliseconds.
	DelayDeviation float64
	// Shaped bandwidth in bytes per second. 0 means unlimited.
	BandwidthBytesPerSec uint64
	// Independent random loss rate in [0, 1].
	RandomLossRate float64
	// Probability of starting a burst loss event in [0, 1].
	BurstLossRate float64
	// Number of packets dropped in a burst event.
	BurstLength int
	// Probability to inject reordering delay in [0, 1].
	ReorderRate float64
	// Maximum extra delay added for reordering.
	MaxReorderDelay time.Duration
	// Receive queue size.
	QueueSize int
	// Random seed. 0 means time-based seed.
	Seed int64
}

// Stats is a snapshot of per-connection counters.
type Stats struct {
	DropRandom   uint64
	DropBurst    uint64
	DropQueue    uint64
	SentPackets  uint64
	RecvPackets  uint64
	SentBytes    uint64
	RecvBytes    uint64
	MaxDelay     time.Duration
	Reordered    uint64
	DelayedSends uint64
}

// LossyConn implements a net.LossyConn with a given loss rate for sending
type LossyConn struct {
	die chan struct{}
	mu  sync.Mutex

	cfg Config

	rx   []Packet // incoming packets
	addr net.Addr // local address

	rdDeadLine      atomic.Value  // read deadline
	wtDeadLine      atomic.Value  // write deadline
	chNotifyReaders chan struct{} // notify incoming packets

	rng *rand.Rand

	// timed sender
	sender *TimedSender
	// serialization state for bandwidth shaping
	nextSend time.Time
	// compatibility delay deviation override in milliseconds
	deviation atomic.Value
	// burst state
	burstRemain int

	// stats on this connection
	sDropRandom    uint64
	sDropBurst     uint64
	sDropQueue     uint64
	sReordered     uint64
	sDelayedSend   uint64
	sSent          uint64
	sReceived      uint64
	sBytesSent     uint64
	sBytesReceived uint64
	sMaxDelay      int64
}

// NewLossyConn create a loss connection with loss rate and latency
//
// loss must be between [0,1]
//
// delay is time in millisecond
func NewLossyConn(loss float64, delay int) (*LossyConn, error) {
	return NewWithConfig(Config{
		Delay:          time.Duration(delay) * time.Millisecond,
		DelayDeviation: 1.0,
		RandomLossRate: loss,
	})
}

// NewLossyPacketConn is kept for backward compatibility.
//
// Deprecated: use NewLossyConn or NewWithConfig.
func NewLossyPacketConn(loss float64, delay int) (*LossyConn, error) {
	return NewLossyConn(loss, delay)
}

// NewWithConfig creates a configurable lossy packet connection.
func NewWithConfig(cfg Config) (*LossyConn, error) {
	if cfg.Delay < 0 {
		return nil, errors.New("delay must be >= 0")
	}
	if cfg.DelayDeviation < 0 {
		return nil, errors.New("delay deviation must be >= 0")
	}
	if cfg.RandomLossRate < 0 || cfg.RandomLossRate > 1 {
		return nil, errors.New("random loss rate must be in [0,1]")
	}
	if cfg.BurstLossRate < 0 || cfg.BurstLossRate > 1 {
		return nil, errors.New("burst loss rate must be in [0,1]")
	}
	if cfg.BurstLength < 0 {
		return nil, errors.New("burst length must be >= 0")
	}
	if cfg.ReorderRate < 0 || cfg.ReorderRate > 1 {
		return nil, errors.New("reorder rate must be in [0,1]")
	}
	if cfg.MaxReorderDelay < 0 {
		return nil, errors.New("max reorder delay must be >= 0")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 4096
	}
	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}

	conn := new(LossyConn)
	conn.cfg = cfg
	conn.chNotifyReaders = make(chan struct{}, 1)
	conn.die = make(chan struct{})
	conn.addr = NewAddress()
	conn.deviation.Store(cfg.DelayDeviation)
	conn.sender = NewTimedSender()
	conn.rng = rand.New(rand.NewSource(cfg.Seed))
	conn.rx = make([]Packet, 0, cfg.QueueSize)
	defaultConnectionManager.Set(conn)
	return conn, nil
}

// SetDelayDeviation sets the deviation for delays, delay is the median value
//
// Default : 1.0
func (conn *LossyConn) SetDelayDeviation(deviation float64) { conn.deviation.Store(deviation) }

func (conn *LossyConn) notifyReaders() {
	select {
	case conn.chNotifyReaders <- struct{}{}:
	default:
	}
}

// simulate receiving a packet internally
func (conn *LossyConn) receivePacket(packet Packet) {
	conn.mu.Lock()
	if conn.cfg.QueueSize > 0 && len(conn.rx) >= conn.cfg.QueueSize {
		conn.mu.Unlock()
		atomic.AddUint64(&conn.sDropQueue, 1)
		return
	}
	conn.rx = append(conn.rx, packet)
	conn.mu.Unlock()
	conn.notifyReaders()
	atomic.AddUint64(&conn.sReceived, 1)
	atomic.AddUint64(&conn.sBytesReceived, uint64(len(packet.payload)))
	delay := time.Now().Sub(packet.ts)
	if time.Now().Sub(packet.ts) > time.Duration(atomic.LoadInt64(&conn.sMaxDelay)) {
		atomic.StoreInt64(&conn.sMaxDelay, int64(delay))
	}
}

// Implementation of the Conn interface.

// Read implements the Conn Read method.
func (conn *LossyConn) Read(b []byte) (int, error) {
	n, _, err := conn.ReadFrom(b)
	return n, err
}

// ReadFrom reads a packet from the connection,
// copying the payload into p. It returns the number of
// bytes copied into p and the return address that
// was on the packet.
// It returns the number of bytes read (0 <= n <= len(p))
// and any error encountered. Callers should always process
// the n > 0 bytes returned before considering the error err.
// ReadFrom can be made to time out and return
// an Error with Timeout() == true after a fixed time limit;
// see SetDeadline and SetReadDeadline.
func (conn *LossyConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	for {
		conn.mu.Lock()
		if len(conn.rx) > 0 {
			n = copy(p, conn.rx[0].payload)
			addr = conn.rx[0].addr
			conn.rx = conn.rx[1:]
			conn.mu.Unlock()
			return
		}

		var timer *time.Timer
		var deadline <-chan time.Time
		if d, ok := conn.rdDeadLine.Load().(time.Time); ok && !d.IsZero() {
			timer = time.NewTimer(time.Until(d))
			deadline = timer.C
		}
		conn.mu.Unlock()

		select {
		case <-deadline:
			return 0, nil, errors.New("i/o timeout")
		case <-conn.die:
			return 0, nil, errors.New("broken pipe")
		case <-conn.chNotifyReaders:
		}

		if timer != nil {
			timer.Stop()
		}
	}
}

// Write implements the Conn Write method.
func (conn *LossyConn) Write(p []byte) (n int, err error) {
	return 0, io.ErrNoProgress
}

// WriteTo writes a packet with payload p to addr.
// WriteTo can be made to time out and return
// an Error with Timeout() == true after a fixed time limit;
// see SetDeadline and SetWriteDeadline.
// On packet-oriented connections, write timeouts are rare.
func (conn *LossyConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// close
	select {
	case <-conn.die:
		return 0, io.EOF
	default:
	}

	if d, ok := conn.wtDeadLine.Load().(time.Time); ok && !d.IsZero() && time.Now().After(d) {
		return 0, errors.New("i/o timeout")
	}
	if addr == nil {
		return 0, errors.New("nil address")
	}

	if remote := defaultConnectionManager.Get(addr.String()); remote != nil {
		conn.mu.Lock()
		if conn.shouldDropLocked() {
			conn.mu.Unlock()
			atomic.AddUint64(&conn.sSent, 1)
			atomic.AddUint64(&conn.sBytesSent, uint64(len(p)))
			return len(p), nil
		}

		now := time.Now()
		start := now
		if conn.nextSend.After(start) {
			start = conn.nextSend
		}
		serialization := time.Duration(0)
		if conn.cfg.BandwidthBytesPerSec > 0 {
			serialization = time.Duration((uint64(len(p))*uint64(time.Second) + conn.cfg.BandwidthBytesPerSec - 1) / conn.cfg.BandwidthBytesPerSec)
		}
		conn.nextSend = start.Add(serialization)
		sendDelay := conn.nextSend.Sub(now) + conn.baseDelayLocked()
		if conn.shouldReorderLocked() {
			sendDelay += conn.reorderDelayLocked()
			atomic.AddUint64(&conn.sReordered, 1)
		}
		if sendDelay > 0 {
			atomic.AddUint64(&conn.sDelayedSend, 1)
		}
		conn.mu.Unlock()

		p1 := make([]byte, len(p))
		copy(p1, p)
		conn.sender.Send(remote, Packet{conn.LocalAddr(), p1, time.Now()}, sendDelay)
		atomic.AddUint64(&conn.sSent, 1)
		atomic.AddUint64(&conn.sBytesSent, uint64(len(p)))
		return len(p), nil
	} else {
		return 0, io.ErrUnexpectedEOF
	}
}

// Close closes the connection.
// Any blocked ReadFrom or WriteTo operations will be unblocked and return errors.
func (conn *LossyConn) Close() error {
	defaultConnectionManager.Delete(conn)
	conn.mu.Lock()
	defer conn.mu.Unlock()

	select {
	case <-conn.die:
		return errors.New("broken pipe")
	default:
		close(conn.die)
		return nil
	}
}

// LocalAddr returns the local network address.
func (conn *LossyConn) LocalAddr() net.Addr { return conn.addr }

// RemoteAddr returns the remote network address.
func (conn *LossyConn) RemoteAddr() net.Addr { return nil }

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail with a timeout (see type Error) instead of
// blocking. The deadline applies to all future and pending
// I/O, not just the immediately following call to ReadFrom or
// WriteTo. After a deadline has been exceeded, the connection
// can be refreshed by setting a deadline in the future.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful ReadFrom or WriteTo calls.
//
// A zero value for t means I/O operations will not time out.
func (conn *LossyConn) SetDeadline(t time.Time) error {
	conn.rdDeadLine.Store(t)
	conn.wtDeadLine.Store(t)
	return nil
}

// SetReadDeadline sets the deadline for future ReadFrom calls
// and any currently-blocked ReadFrom call.
// A zero value for t means ReadFrom will not time out.
func (conn *LossyConn) SetReadDeadline(t time.Time) error {
	conn.rdDeadLine.Store(t)
	return nil
}

// SetWriteDeadline sets the deadline for future WriteTo calls
// and any currently-blocked WriteTo call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means WriteTo will not time out.
func (conn *LossyConn) SetWriteDeadline(t time.Time) error {
	conn.wtDeadLine.Store(t)
	return nil
}

func (conn *LossyConn) String() string {
	return fmt.Sprintf("packet dropped(random):%v dropped(burst):%v dropped(queue):%v packet sent:%v packet received:%v tx bytes:%v rx bytes:%v reordered:%v delayed sends:%v max delay:%v",
		atomic.LoadUint64(&conn.sDropRandom),
		atomic.LoadUint64(&conn.sDropBurst),
		atomic.LoadUint64(&conn.sDropQueue),
		atomic.LoadUint64(&conn.sSent),
		atomic.LoadUint64(&conn.sReceived),
		atomic.LoadUint64(&conn.sBytesSent),
		atomic.LoadUint64(&conn.sBytesReceived),
		atomic.LoadUint64(&conn.sReordered),
		atomic.LoadUint64(&conn.sDelayedSend),
		time.Duration(atomic.LoadInt64(&conn.sMaxDelay)),
	)
}

// Stats returns per-connection counters.
func (conn *LossyConn) Stats() Stats {
	return Stats{
		DropRandom:   atomic.LoadUint64(&conn.sDropRandom),
		DropBurst:    atomic.LoadUint64(&conn.sDropBurst),
		DropQueue:    atomic.LoadUint64(&conn.sDropQueue),
		SentPackets:  atomic.LoadUint64(&conn.sSent),
		RecvPackets:  atomic.LoadUint64(&conn.sReceived),
		SentBytes:    atomic.LoadUint64(&conn.sBytesSent),
		RecvBytes:    atomic.LoadUint64(&conn.sBytesReceived),
		MaxDelay:     time.Duration(atomic.LoadInt64(&conn.sMaxDelay)),
		Reordered:    atomic.LoadUint64(&conn.sReordered),
		DelayedSends: atomic.LoadUint64(&conn.sDelayedSend),
	}
}

// ResetStats resets all counters to zero.
func (conn *LossyConn) ResetStats() {
	atomic.StoreUint64(&conn.sDropRandom, 0)
	atomic.StoreUint64(&conn.sDropBurst, 0)
	atomic.StoreUint64(&conn.sDropQueue, 0)
	atomic.StoreUint64(&conn.sReordered, 0)
	atomic.StoreUint64(&conn.sDelayedSend, 0)
	atomic.StoreUint64(&conn.sSent, 0)
	atomic.StoreUint64(&conn.sReceived, 0)
	atomic.StoreUint64(&conn.sBytesSent, 0)
	atomic.StoreUint64(&conn.sBytesReceived, 0)
	atomic.StoreInt64(&conn.sMaxDelay, 0)
}

func (conn *LossyConn) shouldDropLocked() bool {
	if conn.burstRemain > 0 {
		conn.burstRemain--
		atomic.AddUint64(&conn.sDropBurst, 1)
		return true
	}

	if conn.cfg.BurstLossRate > 0 && conn.cfg.BurstLength > 0 && conn.rng.Float64() < conn.cfg.BurstLossRate {
		conn.burstRemain = conn.cfg.BurstLength - 1
		atomic.AddUint64(&conn.sDropBurst, 1)
		return true
	}

	if conn.cfg.RandomLossRate > 0 && conn.rng.Float64() < conn.cfg.RandomLossRate {
		atomic.AddUint64(&conn.sDropRandom, 1)
		return true
	}

	return false
}

func (conn *LossyConn) baseDelayLocked() time.Duration {
	stddev := conn.deviation.Load().(float64)
	ms := float64(conn.cfg.Delay/time.Millisecond) + stddev*conn.rng.NormFloat64()
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

func (conn *LossyConn) shouldReorderLocked() bool {
	return conn.cfg.ReorderRate > 0 && conn.cfg.MaxReorderDelay > 0 && conn.rng.Float64() < conn.cfg.ReorderRate
}

func (conn *LossyConn) reorderDelayLocked() time.Duration {
	return time.Duration(conn.rng.Int63n(int64(conn.cfg.MaxReorderDelay) + 1))
}
