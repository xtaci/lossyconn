// Package lossyconn is a lossy connection simulator for Golang.
//
// lossyconn provides packet oriented and stream oriented
// lossy connection for testing purpose
package lossyconn

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Address defines a simulated lossy conn address
type Address struct {
	str string
}

func NewAddress() *Address {
	addr := new(Address)
	fakeaddr := make([]byte, 16)
	io.ReadFull(rand.Reader, fakeaddr)
	addr.str = fmt.Sprintf("%s", fakeaddr)
	return addr
}

func (addr *Address) Network() string {
	return "lossy"
}
func (addr *Address) String() string {
	return addr.str
}

type Packet struct {
	addr    net.Addr
	payload []byte
	ts      time.Time
}

// LossPacket implements a net.PacketConn with a given loss rate for sending
type LossyPacketConn struct {
	loss  int
	delay int

	deviation atomic.Value // delay deviation

	rx   []Packet
	addr net.Addr

	die             chan struct{}
	mu              sync.Mutex
	rdDeadLine      atomic.Value
	wtDeadLine      atomic.Value
	chNotifyReaders chan struct{}

	delayedWriter *DelayedWriter

	// stats
	sDrop          uint32
	sSent          uint32
	sReceived      uint32
	sBytesSent     uint32
	sBytesReceived uint32
}

// NewLossyPacketConn create a loss connection with loss rate and latency
//
// loss must be between [0,1]
//
// delay is time in millisecond
func NewLossyPacketConn(loss float64, delay int) (*LossyPacketConn, error) {
	if loss < 0 || loss > 1 {
		return nil, errors.New("loss must be in [0,1]")
	}

	conn := new(LossyPacketConn)
	conn.loss = int(loss * 100)
	conn.delay = delay
	conn.chNotifyReaders = make(chan struct{}, 1)
	conn.die = make(chan struct{})
	conn.addr = NewAddress()
	conn.deviation.Store(float64(1.0))
	conn.delayedWriter = NewDelayedWriter()
	packetConns.Set(conn)
	return conn, nil
}

// SetDelayDeviation sets the deviation for delays, delay is the median value
func (conn *LossyPacketConn) SetDelayDeviation(deviation float64) { conn.deviation.Store(deviation) }

func (conn *LossyPacketConn) notifyReaders() {
	select {
	case conn.chNotifyReaders <- struct{}{}:
	default:
	}
}

// simulate receiving a packet internally
func (conn *LossyPacketConn) receivePacket(packet Packet) {
	conn.mu.Lock()
	conn.rx = append(conn.rx, packet)
	conn.mu.Unlock()
	conn.notifyReaders()
	atomic.AddUint32(&conn.sReceived, 1)
	atomic.AddUint32(&conn.sBytesReceived, uint32(len(packet.payload)))
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
func (conn *LossyPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
RETRY:
	conn.mu.Lock()

	if len(conn.rx) > 0 {
		n = copy(p, conn.rx[0].payload)
		addr = conn.rx[0].addr
		conn.rx = conn.rx[1:]
		conn.mu.Unlock()
		return
	}

	var deadline <-chan time.Time
	if d, ok := conn.rdDeadLine.Load().(time.Time); ok && !d.IsZero() {
		timer := time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}
	conn.mu.Unlock()

	select {
	case <-deadline:
		return 0, nil, errors.New("i/o timeout")
	case <-conn.chNotifyReaders:
		goto RETRY
	case <-conn.die:
		return 0, nil, errors.New("broken pipe")
	}
}

// WriteTo writes a packet with payload p to addr.
// WriteTo can be made to time out and return
// an Error with Timeout() == true after a fixed time limit;
// see SetDeadline and SetWriteDeadline.
// On packet-oriented connections, write timeouts are rare.
func (conn *LossyPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// close
	select {
	case <-conn.die:
		return 0, io.EOF
	default:
	}

	// drop
	if mrand.Intn(100) < conn.loss {
		atomic.AddUint32(&conn.sDrop, 1)
		return len(p), nil
	}

	if remote := packetConns.Get(addr.String()); remote != nil {
		p1 := make([]byte, len(p))
		copy(p1, p)
		delay := float64(conn.delay) + conn.deviation.Load().(float64)*mrand.NormFloat64()
		conn.delayedWriter.Send(remote, Packet{conn.LocalAddr(), p1, time.Now()}, time.Duration(delay)*time.Millisecond)
		atomic.AddUint32(&conn.sSent, 1)
		atomic.AddUint32(&conn.sBytesSent, uint32(len(p)))
		return len(p), nil
	} else {
		return 0, io.ErrUnexpectedEOF
	}
}

// Close closes the connection.
// Any blocked ReadFrom or WriteTo operations will be unblocked and return errors.
func (conn *LossyPacketConn) Close() error {
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
func (conn *LossyPacketConn) LocalAddr() net.Addr { return conn.addr }

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
func (conn *LossyPacketConn) SetDeadline(t time.Time) error {
	conn.rdDeadLine.Store(t)
	conn.wtDeadLine.Store(t)
	return nil
}

// SetReadDeadline sets the deadline for future ReadFrom calls
// and any currently-blocked ReadFrom call.
// A zero value for t means ReadFrom will not time out.
func (conn *LossyPacketConn) SetReadDeadline(t time.Time) error {
	conn.rdDeadLine.Store(t)
	return nil
}

// SetWriteDeadline sets the deadline for future WriteTo calls
// and any currently-blocked WriteTo call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means WriteTo will not time out.
func (conn *LossyPacketConn) SetWriteDeadline(t time.Time) error {
	conn.wtDeadLine.Store(t)
	return nil
}

func (conn *LossyPacketConn) String() string {
	return fmt.Sprintf("packet dropped:%v packet sent:%v packet received:%v tx bytes:%v rx bytes:%v",
		atomic.LoadUint32(&conn.sDrop),
		atomic.LoadUint32(&conn.sSent),
		atomic.LoadUint32(&conn.sReceived),
		atomic.LoadUint32(&conn.sBytesSent),
		atomic.LoadUint32(&conn.sBytesReceived),
	)
}
