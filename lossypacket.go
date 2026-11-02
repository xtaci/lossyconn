// Package lossyconn provides an in-memory packet connection simulator.
//
// It supports configurable delay, bandwidth limits, packet loss, packet size
// and lifetime limits, duplication, corruption, reordering, and bounded queues
// for network testing.
package lossyconn

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Address is the opaque local address of a simulated connection.
type Address struct {
	str string
}

// NewAddress creates a simulated address using cryptographically random bytes.
func NewAddress() *Address {
	addr := new(Address)
	fakeaddr := make([]byte, 16)
	io.ReadFull(crand.Reader, fakeaddr)
	addr.str = fmt.Sprintf("%s", fakeaddr)
	return addr
}

// Network returns the simulated network name.
func (addr *Address) Network() string {
	return "lossy"
}

// String returns the opaque simulated address.
func (addr *Address) String() string {
	return addr.str
}

// Packet is a payload scheduled for delivery to a simulated connection.
type Packet struct {
	addr      net.Addr
	payload   []byte
	ts        time.Time
	expiresAt time.Time
}

// Config controls link simulation behavior.
type Config struct {
	// One-way base delay.
	Delay time.Duration
	// Standard deviation of normally distributed delay variation, in milliseconds.
	DelayDeviation float64
	// Maximum outgoing bandwidth in bytes per second. Zero disables shaping.
	BandwidthBytesPerSec uint64
	// Maximum outgoing payload size in bytes. Zero disables the limit.
	MaxPacketSize int
	// Maximum time a packet may remain in transit. Zero disables expiration.
	PacketLifetime time.Duration
	// Added delay per packet already waiting in the outgoing queue.
	QueueDelayPerPacket time.Duration
	// Maximum congestion delay. Required when QueueDelayPerPacket is positive.
	MaxQueueDelay time.Duration
	// Probability of independently dropping each outgoing packet, in [0, 1].
	RandomLossRate float64
	// Probability of starting a burst-loss event for an outgoing packet, in [0, 1].
	BurstLossRate float64
	// Number of consecutive packets dropped when a burst-loss event starts.
	BurstLength int
	// Probability of duplicating an outgoing packet, in [0, 1].
	DuplicateRate float64
	// Probability of corrupting an outgoing packet payload, in [0, 1].
	CorruptionRate float64
	// Probability of adding an extra delay to an outgoing packet, in [0, 1].
	ReorderRate float64
	// Maximum extra delay used when reordering a packet.
	MaxReorderDelay time.Duration
	// Maximum number of received packets buffered by each connection. Zero uses the default.
	QueueSize int
	// Maximum number of packets waiting in the outgoing queue. Zero uses the default.
	MaxPendingPackets int
	// Seed for deterministic simulation. Zero selects a time-based seed.
	Seed int64
}

// Stats is a snapshot of a connection's packet, byte, loss, and delay counters.
type Stats struct {
	// Packets discarded by independent random loss.
	DropRandom uint64
	// Packets discarded by burst loss.
	DropBurst uint64
	// Packets discarded because the receive queue was full.
	DropQueue uint64
	// Packets discarded because their payload exceeded MaxPacketSize.
	DropOversize uint64
	// Packets discarded after their configured lifetime elapsed in transit.
	DropExpired uint64
	// Packets discarded while the outgoing link was disabled.
	DropLinkDown uint64
	// Additional packet copies scheduled by simulated duplication.
	DuplicatedPackets uint64
	// Accepted outgoing packets whose payload was changed by simulated corruption.
	CorruptedPackets uint64
	// Writes rejected because the outgoing queue was full or its sender was closed.
	DropPendingQueue uint64
	// Packets accepted by WriteTo, including simulated losses.
	SentPackets uint64
	// Packets accepted into the receive queue.
	RecvPackets uint64
	// Payload bytes accepted by WriteTo.
	SentBytes uint64
	// Payload bytes accepted into the receive queue.
	RecvBytes uint64
	// Largest observed delivery delay.
	MaxDelay time.Duration
	// Packets given an extra delay for reordering.
	Reordered uint64
	// Packets given additional congestion delay from the pending send queue.
	QueueDelayedPackets uint64
	// Packets scheduled for delivery after a non-zero delay.
	DelayedSends uint64
}

// LossyConn is an in-memory net.PacketConn with configurable network effects.
type LossyConn struct {
	die chan struct{}
	mu  sync.Mutex

	cfg Config

	rx   []Packet // incoming packets
	addr net.Addr // local address

	rdDeadLine      atomic.Value  // read deadline
	wtDeadLine      atomic.Value  // write deadline
	chNotifyReaders chan struct{} // notify incoming packets
	chDeadline      chan struct{} // notify blocked readers about deadline changes

	rng *rand.Rand

	// timed sender
	sender *TimedSender
	// serialization state for bandwidth shaping
	nextSend time.Time
	// compatibility delay deviation override in milliseconds
	deviation atomic.Value
	// burst state
	burstRemain int
	linkUp      bool

	// stats on this connection
	sDropRandom       uint64
	sDropBurst        uint64
	sDropQueue        uint64
	sDropOversize     uint64
	sDropExpired      uint64
	sDropLinkDown     uint64
	sDuplicated       uint64
	sCorrupted        uint64
	sDropPendingQueue uint64
	sReordered        uint64
	sDelayedSend      uint64
	sSent             uint64
	sReceived         uint64
	sBytesSent        uint64
	sBytesReceived    uint64
	sMaxDelay         int64
	sQueueDelayed     uint64
}

// NewLossyConn creates a connection with a packet-loss probability and delay.
//
// loss must be in [0, 1]. delay is the one-way delay in milliseconds.
//
// Use NewWithConfig to configure additional network effects.
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

// NewWithConfig creates a configurable lossy packet connection. Invalid
// probabilities, negative delay and packet limits, and negative outgoing queue
// limits return an error. Non-positive receive queue limits and zero outgoing
// limits select the default capacity of 4096 packets.
func NewWithConfig(cfg Config) (*LossyConn, error) {
	if cfg.Delay < 0 {
		return nil, errors.New("delay must be >= 0")
	}
	if cfg.MaxPacketSize < 0 {
		return nil, errors.New("maximum packet size must be >= 0")
	}
	if cfg.PacketLifetime < 0 {
		return nil, errors.New("packet lifetime must be >= 0")
	}
	if cfg.QueueDelayPerPacket < 0 {
		return nil, errors.New("queue delay per packet must be >= 0")
	}
	if cfg.MaxQueueDelay < 0 {
		return nil, errors.New("maximum queue delay must be >= 0")
	}
	if cfg.QueueDelayPerPacket > 0 && cfg.MaxQueueDelay == 0 {
		return nil, errors.New("maximum queue delay must be > 0 when queue delay is enabled")
	}
	if math.IsNaN(cfg.DelayDeviation) || math.IsInf(cfg.DelayDeviation, 0) || cfg.DelayDeviation < 0 {
		return nil, errors.New("delay deviation must be finite and >= 0")
	}
	if math.IsNaN(cfg.RandomLossRate) || cfg.RandomLossRate < 0 || cfg.RandomLossRate > 1 {
		return nil, errors.New("random loss rate must be in [0,1]")
	}
	if math.IsNaN(cfg.BurstLossRate) || cfg.BurstLossRate < 0 || cfg.BurstLossRate > 1 {
		return nil, errors.New("burst loss rate must be in [0,1]")
	}
	if cfg.BurstLength < 0 {
		return nil, errors.New("burst length must be >= 0")
	}
	if math.IsNaN(cfg.DuplicateRate) || cfg.DuplicateRate < 0 || cfg.DuplicateRate > 1 {
		return nil, errors.New("duplicate rate must be in [0,1]")
	}
	if math.IsNaN(cfg.CorruptionRate) || cfg.CorruptionRate < 0 || cfg.CorruptionRate > 1 {
		return nil, errors.New("corruption rate must be in [0,1]")
	}
	if math.IsNaN(cfg.ReorderRate) || cfg.ReorderRate < 0 || cfg.ReorderRate > 1 {
		return nil, errors.New("reorder rate must be in [0,1]")
	}
	if cfg.MaxReorderDelay < 0 {
		return nil, errors.New("max reorder delay must be >= 0")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 4096
	}
	if cfg.MaxPendingPackets < 0 {
		return nil, errors.New("maximum pending packets must be >= 0")
	}
	if cfg.MaxPendingPackets == 0 {
		cfg.MaxPendingPackets = 4096
	}
	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}

	conn := new(LossyConn)
	conn.cfg = cfg
	conn.linkUp = true
	conn.chNotifyReaders = make(chan struct{}, 1)
	conn.chDeadline = make(chan struct{}, 1)
	conn.die = make(chan struct{})
	conn.addr = NewAddress()
	conn.deviation.Store(cfg.DelayDeviation)
	conn.sender = newTimedSender(cfg.MaxPendingPackets)
	conn.rng = rand.New(rand.NewSource(cfg.Seed))
	conn.rx = make([]Packet, 0, cfg.QueueSize)
	defaultConnectionManager.Set(conn)
	return conn, nil
}

// SetDelayDeviation sets the delay variation's standard deviation in milliseconds.
// The value must be finite and non-negative. The configured base delay is unchanged.
func (conn *LossyConn) SetDelayDeviation(deviation float64) error {
	if math.IsNaN(deviation) || math.IsInf(deviation, 0) || deviation < 0 {
		return errors.New("delay deviation must be a finite value >= 0")
	}
	conn.deviation.Store(deviation)
	return nil
}

func (conn *LossyConn) notifyReaders() {
	select {
	case conn.chNotifyReaders <- struct{}{}:
	default:
	}
}

// receivePacket queues a delivered packet unless the connection is closed or full.
func (conn *LossyConn) receivePacket(packet Packet) {
	if !packet.expiresAt.IsZero() && !time.Now().Before(packet.expiresAt) {
		atomic.AddUint64(&conn.sDropExpired, 1)
		return
	}

	conn.mu.Lock()
	select {
	case <-conn.die:
		conn.mu.Unlock()
		return
	default:
	}
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
	delay := time.Since(packet.ts)
	for observed := atomic.LoadInt64(&conn.sMaxDelay); int64(delay) > observed; observed = atomic.LoadInt64(&conn.sMaxDelay) {
		if atomic.CompareAndSwapInt64(&conn.sMaxDelay, observed, int64(delay)) {
			break
		}
	}
}

// Read implements the net.Conn Read method.
func (conn *LossyConn) Read(b []byte) (int, error) {
	n, _, err := conn.ReadFrom(b)
	return n, err
}

// ReadFrom reads one packet, copying its payload into p and returning its source
// address. The returned byte count is between zero and len(p); if p is too small,
// the remainder of the packet is discarded. If no packet is available before a
// configured read deadline, the method returns a net.Error with Timeout() == true.
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
			if current, ok := conn.rdDeadLine.Load().(time.Time); ok && (current.IsZero() || time.Now().Before(current)) {
				continue
			}
			return 0, nil, timeoutError("read")
		case <-conn.die:
			stopTimer(timer)
			return 0, nil, errors.New("broken pipe")
		case <-conn.chNotifyReaders:
		case <-conn.chDeadline:
		}

		stopTimer(timer)
	}
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// Write implements net.Conn Write. LossyConn is packet-oriented, so stream writes
// are unsupported.
func (conn *LossyConn) Write(p []byte) (n int, err error) {
	return 0, io.ErrNoProgress
}

// WriteTo schedules p for delivery to addr. It returns an error if addr is not
// reachable, the write deadline has expired, the bounded outgoing queue is full,
// or the connection is closing. Simulated packet loss is reported as a successful
// write, as with a network send.
func (conn *LossyConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	select {
	case <-conn.die:
		return 0, io.EOF
	default:
	}

	if d, ok := conn.wtDeadLine.Load().(time.Time); ok && !d.IsZero() && time.Now().After(d) {
		return 0, timeoutError("write")
	}
	if addr == nil {
		return 0, errors.New("nil address")
	}

	if remote := defaultConnectionManager.Get(addr.String()); remote != nil {
		conn.mu.Lock()
		if !conn.linkUp {
			conn.mu.Unlock()
			atomic.AddUint64(&conn.sDropLinkDown, 1)
			atomic.AddUint64(&conn.sSent, 1)
			atomic.AddUint64(&conn.sBytesSent, uint64(len(p)))
			return len(p), nil
		}
		if conn.cfg.MaxPacketSize > 0 && len(p) > conn.cfg.MaxPacketSize {
			conn.mu.Unlock()
			atomic.AddUint64(&conn.sDropOversize, 1)
			atomic.AddUint64(&conn.sSent, 1)
			atomic.AddUint64(&conn.sBytesSent, uint64(len(p)))
			return len(p), nil
		}
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
		nextSend := start.Add(serialization)
		sendDelay := addDurationSaturated(nextSend.Sub(now), conn.baseDelayLocked())
		queueDelay := conn.queueDelayLocked(conn.sender.pending())
		sendDelay = addDurationSaturated(sendDelay, queueDelay)
		reordered := conn.shouldReorderLocked()
		if reordered {
			sendDelay = addDurationSaturated(sendDelay, conn.reorderDelayLocked())
		}
		delayed := sendDelay > 0

		p1 := make([]byte, len(p))
		copy(p1, p)
		corrupted := false
		if len(p1) > 0 && conn.cfg.CorruptionRate > 0 && conn.rng.Float64() < conn.cfg.CorruptionRate {
			p1[conn.rng.Intn(len(p1))] ^= byte(1 << uint(conn.rng.Intn(8)))
			corrupted = true
		}
		duplicated := conn.cfg.DuplicateRate > 0 && conn.rng.Float64() < conn.cfg.DuplicateRate
		sentAt := time.Now()
		expiresAt := time.Time{}
		if conn.cfg.PacketLifetime > 0 {
			expiresAt = sentAt.Add(conn.cfg.PacketLifetime)
		}
		packets := []Packet{{addr: conn.LocalAddr(), payload: p1, ts: sentAt, expiresAt: expiresAt}}
		if duplicated {
			packets = append(packets, Packet{addr: conn.LocalAddr(), payload: p1, ts: sentAt, expiresAt: expiresAt})
		}
		queued := conn.sender.trySendBatch(remote, packets, sendDelay)
		if queued {
			conn.nextSend = nextSend
		}
		conn.mu.Unlock()
		if !queued {
			atomic.AddUint64(&conn.sDropPendingQueue, 1)
			return 0, errors.New("pending send queue is full or closed")
		}
		if reordered {
			atomic.AddUint64(&conn.sReordered, 1)
		}
		if delayed {
			atomic.AddUint64(&conn.sDelayedSend, 1)
		}
		if queueDelay > 0 {
			atomic.AddUint64(&conn.sQueueDelayed, 1)
		}
		if duplicated {
			atomic.AddUint64(&conn.sDuplicated, 1)
		}
		if corrupted {
			atomic.AddUint64(&conn.sCorrupted, 1)
		}
		atomic.AddUint64(&conn.sSent, 1)
		atomic.AddUint64(&conn.sBytesSent, uint64(len(p)))
		return len(p), nil
	} else {
		return 0, io.ErrUnexpectedEOF
	}
}

// SetLinkState enables or disables this connection's outgoing link.
// Writes while disabled are accepted and counted, but packets are discarded.
func (conn *LossyConn) SetLinkState(up bool) {
	conn.mu.Lock()
	conn.linkUp = up
	conn.mu.Unlock()
}

func (conn *LossyConn) queueDelayLocked(pending int) time.Duration {
	if pending <= 0 || conn.cfg.QueueDelayPerPacket <= 0 {
		return 0
	}
	perPacket := conn.cfg.QueueDelayPerPacket
	maxDelay := conn.cfg.MaxQueueDelay
	if int64(pending) > int64(maxDelay/perPacket) {
		return maxDelay
	}
	delay := time.Duration(pending) * perPacket
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func addDurationSaturated(a, b time.Duration) time.Duration {
	const maxDuration = time.Duration(1<<63 - 1)
	if b > 0 && a > maxDuration-b {
		return maxDuration
	}
	if b < 0 && a < time.Duration(-1<<63)-b {
		return time.Duration(-1 << 63)
	}
	return a + b
}

// Close closes the connection, unblocks blocked reads, stops its sender, and
// discards packets that have not yet been delivered.
func (conn *LossyConn) Close() error {
	defaultConnectionManager.Delete(conn)
	conn.mu.Lock()
	select {
	case <-conn.die:
		conn.mu.Unlock()
		return errors.New("broken pipe")
	default:
		close(conn.die)
	}
	conn.mu.Unlock()
	return conn.sender.Close()
}

// LocalAddr returns the connection's simulated local address.
func (conn *LossyConn) LocalAddr() net.Addr { return conn.addr }

// RemoteAddr returns nil because a simulated connection can send to multiple peers.
func (conn *LossyConn) RemoteAddr() net.Addr { return nil }

// SetDeadline sets both read and write deadlines. It is equivalent to calling
// SetReadDeadline and SetWriteDeadline with the same absolute time.
//
// A deadline applies to current and future operations. Reads already in progress
// are woken when the deadline changes. A read with no available packet waits until
// data arrives, the connection closes, or the deadline expires. WriteTo checks its
// deadline before scheduling a packet. Setting a later deadline allows subsequent
// operations to proceed.
//
// A zero time disables deadlines.
func (conn *LossyConn) SetDeadline(t time.Time) error {
	conn.rdDeadLine.Store(t)
	conn.wtDeadLine.Store(t)
	conn.notifyDeadlineChange()
	return nil
}

// SetReadDeadline sets the deadline for current and future ReadFrom calls.
// A zero time disables the read deadline.
func (conn *LossyConn) SetReadDeadline(t time.Time) error {
	conn.rdDeadLine.Store(t)
	conn.notifyDeadlineChange()
	return nil
}

func (conn *LossyConn) notifyDeadlineChange() {
	select {
	case conn.chDeadline <- struct{}{}:
	default:
	}
}

// SetWriteDeadline sets the deadline checked by current and future WriteTo calls.
// WriteTo does not block while waiting for delivery. A zero time disables the
// write deadline.
func (conn *LossyConn) SetWriteDeadline(t time.Time) error {
	conn.wtDeadLine.Store(t)
	return nil
}

// String returns a summary of packet, byte, loss, and delay statistics.
func (conn *LossyConn) String() string {
	return fmt.Sprintf("packet dropped(random):%v dropped(burst):%v dropped(queue):%v dropped(oversize):%v dropped(expired):%v dropped(link down):%v dropped(pending queue):%v duplicated:%v corrupted:%v packet sent:%v packet received:%v tx bytes:%v rx bytes:%v reordered:%v queue delayed:%v delayed sends:%v max delay:%v",
		atomic.LoadUint64(&conn.sDropRandom),
		atomic.LoadUint64(&conn.sDropBurst),
		atomic.LoadUint64(&conn.sDropQueue),
		atomic.LoadUint64(&conn.sDropOversize),
		atomic.LoadUint64(&conn.sDropExpired),
		atomic.LoadUint64(&conn.sDropLinkDown),
		atomic.LoadUint64(&conn.sDropPendingQueue),
		atomic.LoadUint64(&conn.sDuplicated),
		atomic.LoadUint64(&conn.sCorrupted),
		atomic.LoadUint64(&conn.sSent),
		atomic.LoadUint64(&conn.sReceived),
		atomic.LoadUint64(&conn.sBytesSent),
		atomic.LoadUint64(&conn.sBytesReceived),
		atomic.LoadUint64(&conn.sReordered),
		atomic.LoadUint64(&conn.sQueueDelayed),
		atomic.LoadUint64(&conn.sDelayedSend),
		time.Duration(atomic.LoadInt64(&conn.sMaxDelay)),
	)
}

// Stats returns a snapshot of the connection's counters.
func (conn *LossyConn) Stats() Stats {
	return Stats{
		DropRandom:          atomic.LoadUint64(&conn.sDropRandom),
		DropBurst:           atomic.LoadUint64(&conn.sDropBurst),
		DropQueue:           atomic.LoadUint64(&conn.sDropQueue),
		DropOversize:        atomic.LoadUint64(&conn.sDropOversize),
		DropExpired:         atomic.LoadUint64(&conn.sDropExpired),
		DropLinkDown:        atomic.LoadUint64(&conn.sDropLinkDown),
		DuplicatedPackets:   atomic.LoadUint64(&conn.sDuplicated),
		CorruptedPackets:    atomic.LoadUint64(&conn.sCorrupted),
		DropPendingQueue:    atomic.LoadUint64(&conn.sDropPendingQueue),
		SentPackets:         atomic.LoadUint64(&conn.sSent),
		RecvPackets:         atomic.LoadUint64(&conn.sReceived),
		SentBytes:           atomic.LoadUint64(&conn.sBytesSent),
		RecvBytes:           atomic.LoadUint64(&conn.sBytesReceived),
		MaxDelay:            time.Duration(atomic.LoadInt64(&conn.sMaxDelay)),
		Reordered:           atomic.LoadUint64(&conn.sReordered),
		QueueDelayedPackets: atomic.LoadUint64(&conn.sQueueDelayed),
		DelayedSends:        atomic.LoadUint64(&conn.sDelayedSend),
	}
}

// ResetStats resets all counters, including the observed maximum delay.
func (conn *LossyConn) ResetStats() {
	atomic.StoreUint64(&conn.sDropRandom, 0)
	atomic.StoreUint64(&conn.sDropBurst, 0)
	atomic.StoreUint64(&conn.sDropQueue, 0)
	atomic.StoreUint64(&conn.sDropOversize, 0)
	atomic.StoreUint64(&conn.sDropExpired, 0)
	atomic.StoreUint64(&conn.sDropLinkDown, 0)
	atomic.StoreUint64(&conn.sDuplicated, 0)
	atomic.StoreUint64(&conn.sCorrupted, 0)
	atomic.StoreUint64(&conn.sDropPendingQueue, 0)
	atomic.StoreUint64(&conn.sReordered, 0)
	atomic.StoreUint64(&conn.sDelayedSend, 0)
	atomic.StoreUint64(&conn.sSent, 0)
	atomic.StoreUint64(&conn.sReceived, 0)
	atomic.StoreUint64(&conn.sBytesSent, 0)
	atomic.StoreUint64(&conn.sBytesReceived, 0)
	atomic.StoreInt64(&conn.sMaxDelay, 0)
	atomic.StoreUint64(&conn.sQueueDelayed, 0)
}

type timeoutError string

func (e timeoutError) Error() string   { return string(e) + ": i/o timeout" }
func (e timeoutError) Timeout() bool   { return true }
func (e timeoutError) Temporary() bool { return true }

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
