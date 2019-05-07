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

var packetConns *PacketConns

func init() {
	packetConns = NewPacketConns()
}

type PacketConns struct {
	conns map[string]*LossyPacketConn
	mu    sync.Mutex
}

func NewPacketConns() *PacketConns {
	pcs := new(PacketConns)
	pcs.conns = make(map[string]*LossyPacketConn)
	return pcs
}

func (pcs *PacketConns) Get(addr string) *LossyPacketConn {
	pcs.mu.Lock()
	defer pcs.mu.Unlock()
	return pcs.conns[addr]
}

func (pcs *PacketConns) Set(conn *LossyPacketConn) {
	pcs.mu.Lock()
	defer pcs.mu.Unlock()
	pcs.conns[conn.LocalAddr().String()] = conn
}

type Packet struct {
	addr    net.Addr
	payload []byte
}

// LossPacket implements a net.PacketConn with a given loss rate for sending
type LossyPacketConn struct {
	loss  int
	delay int
	rx    []Packet
	addr  net.Addr

	die             chan struct{}
	mu              sync.Mutex
	rdDeadLine      atomic.Value
	wtDeadLine      atomic.Value
	chNotifyReaders chan struct{}
}

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

// NewLossyPacketConn create a loss connection with loss rate and latency
// loss must be between [0,1]
// delay is time in millisecond
func NewLossyPacketConn(loss float32, delay int) (*LossyPacketConn, error) {
	if loss < 0 || loss > 1 {
		return nil, errors.New("loss must be in [0,1]")
	}

	lp := new(LossyPacketConn)
	lp.loss = int(loss * 100)
	lp.delay = delay
	lp.chNotifyReaders = make(chan struct{}, 1)
	lp.die = make(chan struct{})
	lp.addr = NewAddress()
	return lp, nil
}

func (lp *LossyPacketConn) notifyReaders() {
	select {
	case lp.chNotifyReaders <- struct{}{}:
	default:
	}
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
func (lp *LossyPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
RETRY:
	lp.mu.Lock()

	if lp.rx != nil {
		n = copy(p, lp.rx[0].payload)
		addr = lp.rx[0].addr
		lp.rx = lp.rx[1:]
		lp.mu.Unlock()
		return
	}

	var deadline <-chan time.Time
	if d, ok := lp.rdDeadLine.Load().(time.Time); ok && !d.IsZero() {
		timer := time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}
	lp.mu.Unlock()

	select {
	case <-deadline:
		return 0, nil, errors.New("i/o timeout")
	case <-lp.chNotifyReaders:
		goto RETRY
	case <-lp.die:
		return 0, nil, errors.New("broken pipe")
	}
}

// WriteTo writes a packet with payload p to addr.
// WriteTo can be made to time out and return
// an Error with Timeout() == true after a fixed time limit;
// see SetDeadline and SetWriteDeadline.
// On packet-oriented connections, write timeouts are rare.
func (lp *LossyPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// close
	select {
	case <-lp.die:
		return 0, errors.New("broken pipe")
	default:
	}

	// timeout
	if d, ok := lp.wtDeadLine.Load().(time.Time); ok && !d.IsZero() {
		if time.Now().After(d) {
			return 0, errors.New("i/o timeout")
		}
	}

	// drop
	if mrand.Intn(100) < lp.loss {
		return len(p), nil
	}

	if remote := packetConns.Get(addr.String()); remote != nil {
		// copy
		c := make([]byte, len(p))
		copy(c, p)
		// delay
		<-time.After(time.Duration(lp.delay) * time.Millisecond)
		remote.mu.Lock()
		remote.rx = append(remote.rx, Packet{lp.LocalAddr(), c})
		remote.mu.Unlock()
		remote.notifyReaders()
	}
	return len(p), nil
}

// Close closes the connection.
// Any blocked ReadFrom or WriteTo operations will be unblocked and return errors.
func (lp *LossyPacketConn) Close() error {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	select {
	case <-lp.die:
		return errors.New("broken pipe")
	default:
		close(lp.die)
		return nil
	}
}

// LocalAddr returns the local network address.
func (lp *LossyPacketConn) LocalAddr() net.Addr { return lp.addr }

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
func (lp *LossyPacketConn) SetDeadline(t time.Time) error {
	lp.rdDeadLine.Store(t)
	lp.wtDeadLine.Store(t)
	return nil
}

// SetReadDeadline sets the deadline for future ReadFrom calls
// and any currently-blocked ReadFrom call.
// A zero value for t means ReadFrom will not time out.
func (lp *LossyPacketConn) SetReadDeadline(t time.Time) error {
	lp.rdDeadLine.Store(t)
	return nil
}

// SetWriteDeadline sets the deadline for future WriteTo calls
// and any currently-blocked WriteTo call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means WriteTo will not time out.
func (lp *LossyPacketConn) SetWriteDeadline(t time.Time) error {
	lp.wtDeadLine.Store(t)
	return nil
}
