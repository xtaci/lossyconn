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

// LossyConn implements a net.LossyConn with a given loss rate for sending
type LossyConn struct {
	die chan struct{}
	mu  sync.Mutex

	loss      int          // loss rate [0, 100]
	delay     int          // mean value for delay
	deviation atomic.Value // delay deviation

	rx   []Packet // incoming packets
	addr net.Addr // local address

	rdDeadLine      atomic.Value  // read deadline
	wtDeadLine      atomic.Value  // write deadline
	chNotifyReaders chan struct{} // notify incoming packets

	rng *rand.Rand

	// timed sender
	sender *TimedSender

	// stats on this connection
	sDrop          uint32
	sSent          uint32
	sReceived      uint32
	sBytesSent     uint32
	sBytesReceived uint32
	sMaxDelay      int64
}

// NewLossyConn create a loss connection with loss rate and latency
//
// loss must be between [0,1]
//
// delay is time in millisecond
func NewLossyConn(loss float64, delay int) (*LossyConn, error) {
	if loss < 0 || loss > 1 {
		return nil, errors.New("loss must be in [0,1]")
	}

	conn := new(LossyConn)
	conn.loss = int(loss * 100)
	conn.delay = delay
	conn.chNotifyReaders = make(chan struct{}, 1)
	conn.die = make(chan struct{})
	conn.addr = NewAddress()
	conn.deviation.Store(float64(1.0))
	conn.sender = NewTimedSender()
	conn.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
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
	conn.rx = append(conn.rx, packet)
	conn.mu.Unlock()
	conn.notifyReaders()
	atomic.AddUint32(&conn.sReceived, 1)
	atomic.AddUint32(&conn.sBytesReceived, uint32(len(packet.payload)))
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

	// drop
	if conn.rng.Intn(100) < conn.loss {
		atomic.AddUint32(&conn.sDrop, 1)
		return len(p), nil
	}

	if remote := defaultConnectionManager.Get(addr.String()); remote != nil {
		p1 := make([]byte, len(p))
		copy(p1, p)
		delay := float64(conn.delay) + conn.deviation.Load().(float64)*rand.NormFloat64()
		conn.sender.Send(remote, Packet{conn.LocalAddr(), p1, time.Now()}, time.Duration(delay)*time.Millisecond)
		atomic.AddUint32(&conn.sSent, 1)
		atomic.AddUint32(&conn.sBytesSent, uint32(len(p)))
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
	return fmt.Sprintf("packet dropped:%v packet sent:%v packet received:%v tx bytes:%v rx bytes:%v max delay:%v",
		atomic.LoadUint32(&conn.sDrop),
		atomic.LoadUint32(&conn.sSent),
		atomic.LoadUint32(&conn.sReceived),
		atomic.LoadUint32(&conn.sBytesSent),
		atomic.LoadUint32(&conn.sBytesReceived),
		time.Duration(atomic.LoadInt64(&conn.sMaxDelay)),
	)
}
