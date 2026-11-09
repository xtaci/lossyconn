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

import (
	"fmt"
	"math"
	"net"
	"testing"
	"time"
)

func TestNewLossyConnInvalidInput(t *testing.T) {
	if _, err := NewLossyConn(-0.1, 10); err == nil {
		t.Fatal("expected invalid loss error")
	}
	if _, err := NewWithConfig(Config{RandomLossRate: 2}); err == nil {
		t.Fatal("expected invalid random loss error")
	}
	if _, err := NewWithConfig(Config{BurstLossRate: -0.1}); err == nil {
		t.Fatal("expected invalid burst loss error")
	}
	if _, err := NewWithConfig(Config{DuplicateRate: 1.1}); err == nil {
		t.Fatal("expected invalid duplicate rate error")
	}
	if _, err := NewWithConfig(Config{CorruptionRate: math.NaN()}); err == nil {
		t.Fatal("expected invalid corruption rate error")
	}
	if _, err := NewWithConfig(Config{RandomLossRate: math.NaN()}); err == nil {
		t.Fatal("expected invalid NaN loss error")
	}
	if _, err := NewWithConfig(Config{DelayDeviation: math.Inf(1)}); err == nil {
		t.Fatal("expected invalid infinite delay deviation")
	}
	if _, err := NewWithConfig(Config{MaxPendingPackets: -1}); err == nil {
		t.Fatal("expected invalid pending queue size")
	}
	if _, err := NewWithConfig(Config{MaxPacketSize: -1}); err == nil {
		t.Fatal("expected invalid maximum packet size")
	}
	if _, err := NewWithConfig(Config{PacketLifetime: -time.Nanosecond}); err == nil {
		t.Fatal("expected invalid packet lifetime")
	}
	if _, err := NewWithConfig(Config{QueueDelayPerPacket: time.Millisecond}); err == nil {
		t.Fatal("expected queue delay without maximum to be invalid")
	}
	if _, err := NewWithConfig(Config{QueueDelayPerPacket: -time.Nanosecond, MaxQueueDelay: time.Second}); err == nil {
		t.Fatal("expected invalid queue delay")
	}
	if _, err := NewWithConfig(Config{QueueDelayPerPacket: time.Millisecond, MaxQueueDelay: -time.Second}); err == nil {
		t.Fatal("expected invalid maximum queue delay")
	}
}

func TestSetDelayDeviationValidation(t *testing.T) {
	conn, err := NewWithConfig(Config{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for _, deviation := range []float64{-1, math.NaN(), math.Inf(1)} {
		if err := conn.SetDelayDeviation(deviation); err == nil {
			t.Fatalf("expected invalid deviation error for %v", deviation)
		}
	}
	if err := conn.SetDelayDeviation(0); err != nil {
		t.Fatalf("expected zero deviation to be valid: %v", err)
	}
}

func TestNewLossyPacketConnCompatibility(t *testing.T) {
	left, err := NewLossyPacketConn(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewLossyPacketConn(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	defer right.Close()

	payload := []byte("compat")
	if _, err := left.WriteTo(payload, right.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = right.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	if _, _, err := right.ReadFrom(buf); err != nil {
		t.Fatal(err)
	}
}

func TestRandomLossDropStats(t *testing.T) {
	left, err := NewWithConfig(Config{
		RandomLossRate: 1,
		Delay:          0,
		Seed:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	if _, err := left.WriteTo([]byte{1}, right.LocalAddr()); err != nil {
		t.Fatal(err)
	}

	_ = right.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	buf := make([]byte, 1)
	if _, _, err := right.ReadFrom(buf); err == nil {
		t.Fatal("expected timeout on dropped packet")
	}

	st := left.Stats()
	if st.DropRandom == 0 {
		t.Fatalf("expected random drop stats, got %+v", st)
	}
}

func TestBurstLossDropStats(t *testing.T) {
	left, err := NewWithConfig(Config{
		BurstLossRate: 1,
		BurstLength:   3,
		Delay:         0,
		Seed:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	for i := 0; i < 3; i++ {
		if _, err := left.WriteTo([]byte{byte(i)}, right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}

	_ = right.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	buf := make([]byte, 1)
	if _, _, err := right.ReadFrom(buf); err == nil {
		t.Fatal("expected timeout on burst-dropped packets")
	}

	st := left.Stats()
	if st.DropBurst < 3 {
		t.Fatalf("expected burst drop stats, got %+v", st)
	}
}

func TestPacketDuplicationAndCorruption(t *testing.T) {
	left, err := NewWithConfig(Config{
		DelayDeviation: 0,
		DuplicateRate:  1,
		CorruptionRate: 1,
		Seed:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{DelayDeviation: 0, QueueSize: 4, Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	payload := []byte{0}
	if _, err := left.WriteTo(payload, right.LocalAddr()); err != nil {
		t.Fatal(err)
	}

	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	var received []byte
	for i := 0; i < 2; i++ {
		n, _, err := right.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 || buf[0] == payload[0] {
			t.Fatalf("expected corrupted payload, got %v", buf[:n])
		}
		received = append(received, buf[0])
	}
	if received[0] != received[1] {
		t.Fatalf("duplicate payloads differ: %v", received)
	}

	leftStats := left.Stats()
	rightStats := right.Stats()
	if leftStats.SentPackets != 1 || leftStats.SentBytes != 1 ||
		leftStats.DuplicatedPackets != 1 || leftStats.CorruptedPackets != 1 {
		t.Fatalf("unexpected sender stats: %+v", leftStats)
	}
	if rightStats.RecvPackets != 2 || rightStats.RecvBytes != 2 {
		t.Fatalf("unexpected receiver stats: %+v", rightStats)
	}
	left.ResetStats()
	if stats := left.Stats(); stats.DuplicatedPackets != 0 || stats.CorruptedPackets != 0 {
		t.Fatalf("expected duplication and corruption stats to reset, got %+v", stats)
	}
}

func TestDuplicateRequiresRoomForBothCopies(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:             time.Hour,
		DelayDeviation:    0,
		DuplicateRate:     1,
		MaxPendingPackets: 1,
		Seed:              1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	if _, err := left.WriteTo([]byte("packet"), right.LocalAddr()); err == nil {
		t.Fatal("expected outgoing queue overflow for atomic duplicate")
	}
	stats := left.Stats()
	if stats.SentPackets != 0 || stats.DuplicatedPackets != 0 || stats.DropPendingQueue != 1 {
		t.Fatalf("unexpected stats after rejected duplicate: %+v", stats)
	}
}

func TestMaximumPacketSize(t *testing.T) {
	left, err := NewWithConfig(Config{MaxPacketSize: 3, DelayDeviation: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	for _, payload := range [][]byte{{1, 2, 3}, {4, 5, 6, 7}} {
		if n, err := left.WriteTo(payload, right.LocalAddr()); err != nil || n != len(payload) {
			t.Fatalf("WriteTo(%v) = %d, %v; want %d, nil", payload, n, err, len(payload))
		}
	}
	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	n, _, err := right.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || string(buf[:n]) != string([]byte{1, 2, 3}) {
		t.Fatalf("unexpected delivered packet: %v", buf[:n])
	}
	if _, _, err := right.ReadFrom(buf); !isTimeout(err) {
		t.Fatalf("expected oversized packet to be dropped, got %v", err)
	}

	stats := left.Stats()
	if stats.SentPackets != 2 || stats.SentBytes != 7 || stats.DropOversize != 1 {
		t.Fatalf("unexpected sender stats: %+v", stats)
	}
	if stats := right.Stats(); stats.RecvPackets != 1 || stats.RecvBytes != 3 {
		t.Fatalf("unexpected receiver stats: %+v", stats)
	}
	left.ResetStats()
	if stats := left.Stats(); stats.DropOversize != 0 {
		t.Fatalf("expected oversized-drop stats to reset, got %+v", stats)
	}
}

func TestPacketLifetimeExpiresInTransit(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:          40 * time.Millisecond,
		DelayDeviation: 0,
		PacketLifetime: 10 * time.Millisecond,
		Seed:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	if n, err := left.WriteTo([]byte("expires"), right.LocalAddr()); err != nil || n != len("expires") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("expires"))
	}
	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := right.ReadFrom(make([]byte, 16)); !isTimeout(err) {
		t.Fatalf("expected expired packet to be dropped, got %v", err)
	}

	if got := right.Stats(); got.DropExpired != 1 || got.RecvPackets != 0 {
		t.Fatalf("unexpected receiver stats for expired packet: %+v", got)
	}
	if got := left.Stats(); got.SentPackets != 1 || got.SentBytes != uint64(len("expires")) {
		t.Fatalf("expired packet should count as a successful write: %+v", got)
	}
	right.ResetStats()
	if got := right.Stats(); got.DropExpired != 0 {
		t.Fatalf("expected expired-drop stats to reset, got %+v", got)
	}
}

func TestLinkDownAndRecovery(t *testing.T) {
	left, err := NewWithConfig(Config{DelayDeviation: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	left.SetLinkState(false)
	if n, err := left.WriteTo([]byte("dropped"), right.LocalAddr()); err != nil || n != len("dropped") {
		t.Fatalf("write while link down = %d, %v; want successful write", n, err)
	}
	left.SetLinkState(true)
	if n, err := left.WriteTo([]byte("received"), right.LocalAddr()); err != nil || n != len("received") {
		t.Fatalf("write after link recovery = %d, %v; want successful write", n, err)
	}

	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	n, _, err := right.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "received" {
		t.Fatalf("unexpected delivered payload: %q", buf[:n])
	}
	if _, _, err := right.ReadFrom(buf); !isTimeout(err) {
		t.Fatalf("expected packet sent during outage to be dropped, got %v", err)
	}

	stats := left.Stats()
	if stats.DropLinkDown != 1 || stats.SentPackets != 2 || stats.SentBytes != uint64(len("dropped")+len("received")) {
		t.Fatalf("unexpected sender stats after link recovery: %+v", stats)
	}
	left.ResetStats()
	if stats := left.Stats(); stats.DropLinkDown != 0 {
		t.Fatalf("expected link-down stats to reset, got %+v", stats)
	}
}

func TestPeerProfilesAreIndependent(t *testing.T) {
	left, err := NewWithConfig(Config{DelayDeviation: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	slowPeer, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer slowPeer.Close()
	droppingPeer, err := NewWithConfig(Config{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer droppingPeer.Close()

	if err := left.SetPeerProfile(slowPeer.LocalAddr(), LinkProfile{
		Config: Config{Delay: 100 * time.Millisecond, DelayDeviation: 0},
		Up:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := left.SetPeerProfile(droppingPeer.LocalAddr(), LinkProfile{
		Config: Config{RandomLossRate: 1, DelayDeviation: 0},
		Up:     true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := left.WriteTo([]byte("slow"), slowPeer.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if _, err := left.WriteTo([]byte("drop"), droppingPeer.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if err := slowPeer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	start := time.Now()
	n, _, err := slowPeer.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "slow" || time.Since(start) < 70*time.Millisecond {
		t.Fatalf("peer-specific delay was not applied: payload=%q elapsed=%v", buf[:n], time.Since(start))
	}

	if err := droppingPeer.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := droppingPeer.ReadFrom(buf); !isTimeout(err) {
		t.Fatalf("expected packet loss for configured peer, got %v", err)
	}

	stats := left.Stats()
	if stats.SentPackets != 2 || stats.DropRandom != 1 {
		t.Fatalf("unexpected aggregate sender stats: %+v", stats)
	}
	if err := left.ClearPeerProfile(slowPeer.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if _, err := left.WriteTo([]byte("default"), slowPeer.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if err := slowPeer.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := slowPeer.ReadFrom(buf); err != nil {
		t.Fatalf("cleared profile should restore default link: %v", err)
	}
}

func TestScheduledPeerProfiles(t *testing.T) {
	left, err := NewWithConfig(Config{DelayDeviation: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	if err := left.SetPeerProfile(right.LocalAddr(), LinkProfile{
		Config: Config{DelayDeviation: 0},
		Up:     true,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	downAt := now.Add(80 * time.Millisecond)
	upAt := now.Add(180 * time.Millisecond)
	if err := left.SchedulePeerProfile(right.LocalAddr(), upAt, LinkProfile{
		Config: Config{Delay: 35 * time.Millisecond, DelayDeviation: 0},
		Up:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := left.SchedulePeerProfile(right.LocalAddr(), downAt, LinkProfile{
		Config: Config{DelayDeviation: 0},
		Up:     false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := left.SchedulePeerProfile(right.LocalAddr(), downAt, LinkProfile{
		Config: Config{DelayDeviation: 0},
		Up:     true,
	}); err == nil {
		t.Fatal("expected duplicate schedule time to be rejected")
	}
	if err := left.SchedulePeerProfile(nil, now, LinkProfile{Up: true}); err == nil {
		t.Fatal("expected nil peer address to be rejected")
	}

	if _, err := left.WriteTo([]byte("before"), right.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(downAt) + 10*time.Millisecond)
	if _, err := left.WriteTo([]byte("during"), right.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(upAt) + 10*time.Millisecond)
	if _, err := left.WriteTo([]byte("after"), right.LocalAddr()); err != nil {
		t.Fatal(err)
	}

	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	for i, want := range []string{"before", "after"} {
		start := time.Now()
		n, _, err := right.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if string(buf[:n]) != want {
			t.Fatalf("got packet %q, want %q", buf[:n], want)
		}
		if i == 1 && time.Since(start) < 25*time.Millisecond {
			t.Fatalf("scheduled recovery delay was not applied: elapsed=%v", time.Since(start))
		}
	}
	if _, _, err := right.ReadFrom(buf); !isTimeout(err) {
		t.Fatalf("expected packet scheduled during outage to be dropped, got %v", err)
	}
	if stats := left.Stats(); stats.DropLinkDown != 1 || stats.SentPackets != 3 {
		t.Fatalf("unexpected scheduled-link stats: %+v", stats)
	}
}

func TestCongestionQueueDelayAndCap(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:               250 * time.Millisecond,
		DelayDeviation:      0,
		QueueDelayPerPacket: 30 * time.Millisecond,
		MaxQueueDelay:       45 * time.Millisecond,
		QueueSize:           8,
		Seed:                1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{QueueSize: 8, Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := left.WriteTo([]byte{byte(i)}, right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	for i := 0; i < 3; i++ {
		if _, _, err := right.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 280*time.Millisecond {
		t.Fatalf("congestion queue delay was not applied: elapsed=%v", elapsed)
	}
	if stats := left.Stats(); stats.QueueDelayedPackets == 0 {
		t.Fatalf("expected queued packets to receive congestion delay: %+v", stats)
	}

	left.mu.Lock()
	capped := left.queueDelayLocked(left.cfg, 100)
	left.mu.Unlock()
	if capped != 45*time.Millisecond {
		t.Fatalf("queue delay cap = %v, want %v", capped, 45*time.Millisecond)
	}
	left.ResetStats()
	if stats := left.Stats(); stats.QueueDelayedPackets != 0 {
		t.Fatalf("expected queue-delay stats to reset, got %+v", stats)
	}
}

func TestReadFromTruncatesDatagram(t *testing.T) {
	left, err := NewWithConfig(Config{DelayDeviation: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	payload := []byte("complete datagram")
	if _, err := left.WriteTo(payload, right.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	n, from, err := right.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) || string(buf) != string(payload[:len(buf)]) {
		t.Fatalf("unexpected truncated datagram: n=%d payload=%q", n, buf[:n])
	}
	if from.String() != left.LocalAddr().String() {
		t.Fatalf("unexpected source address: got=%q want=%q", from, left.LocalAddr())
	}
	if _, _, err := right.ReadFrom(buf); !isTimeout(err) {
		t.Fatalf("expected datagram remainder to be discarded, got %v", err)
	}
}

func TestBandwidthAndReorderStats(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:                0,
		DelayDeviation:       0,
		BandwidthBytesPerSec: 100,
		ReorderRate:          1,
		MaxReorderDelay:      5 * time.Millisecond,
		Seed:                 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	for i := 0; i < 4; i++ {
		if _, err := left.WriteTo([]byte{byte(i)}, right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}

	_ = right.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	for i := 0; i < 4; i++ {
		if _, _, err := right.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}

	st := left.Stats()
	if st.Reordered == 0 {
		t.Fatalf("expected reorder stats, got %+v", st)
	}
	if st.DelayedSends == 0 {
		t.Fatalf("expected delayed send stats from shaping, got %+v", st)
	}
}

func TestWriteToUnreachable(t *testing.T) {
	left, err := NewWithConfig(Config{Delay: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	if _, err := left.WriteTo([]byte{1}, fakeAddr("missing")); err == nil {
		t.Fatal("expected unreachable destination error")
	}
}

func TestQueueDropAndResetStats(t *testing.T) {
	left, err := NewWithConfig(Config{Delay: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{
		Delay:     0,
		QueueSize: 1,
		Seed:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	for i := 0; i < 8; i++ {
		if _, err := left.WriteTo([]byte{byte(i)}, right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(20 * time.Millisecond)

	st := right.Stats()
	if st.DropQueue == 0 {
		t.Fatalf("expected queue drops, got %+v", st)
	}

	right.ResetStats()
	st = right.Stats()
	if st.DropQueue != 0 || st.RecvPackets != 0 || st.RecvBytes != 0 {
		t.Fatalf("expected zeroed stats after reset, got %+v", st)
	}
}

func TestPendingSendQueueLimitAndClose(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:             time.Hour,
		DelayDeviation:    0,
		MaxPendingPackets: 1,
		Seed:              1,
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewWithConfig(Config{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	if _, err := left.WriteTo([]byte("queued"), right.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if _, err := left.WriteTo([]byte("overflow"), right.LocalAddr()); err == nil {
		t.Fatal("expected outgoing queue overflow error")
	}
	if got := left.Stats().DropPendingQueue; got != 1 {
		t.Fatalf("unexpected outgoing queue drop count: %d", got)
	}

	if err := left.Close(); err != nil {
		t.Fatal(err)
	}
	if err := right.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := right.ReadFrom(make([]byte, 16)); err == nil {
		t.Fatal("expected pending packet to be discarded on sender close")
	}
}

func TestReadDeadlineCanBeUpdatedWhileBlocked(t *testing.T) {
	conn, err := NewWithConfig(Config{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	result := make(chan error, 1)
	started := make(chan struct{})
	if err := conn.SetReadDeadline(time.Now().Add(120 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	go func() {
		close(started)
		_, _, readErr := conn.ReadFrom(make([]byte, 1))
		result <- readErr
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !conn.sender.trySend(conn, Packet{addr: fakeAddr("sender"), payload: []byte{1}, ts: time.Now()}, 150*time.Millisecond) {
		t.Fatal("failed to schedule packet")
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("read failed after extending deadline: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("read did not complete after packet delivery")
	}
}

func TestReadTimeoutImplementsNetError(t *testing.T) {
	conn, err := NewWithConfig(Config{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadFrom(make([]byte, 1))
	if err == nil {
		t.Fatal("expected read timeout")
	}
	netErr, ok := err.(net.Error)
	if !ok || !netErr.Timeout() {
		t.Fatalf("expected net.Error timeout, got %T: %v", err, err)
	}
}

func TestReadDeadlineAndCloseUnblock(t *testing.T) {
	conn, err := NewWithConfig(Config{Delay: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	buf := make([]byte, 1)
	if _, _, err := conn.ReadFrom(buf); err == nil {
		t.Fatal("expected read timeout")
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("read timeout fired too early: %v", elapsed)
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, _, e := conn.ReadFrom(buf)
		errCh <- e
	}()
	time.Sleep(15 * time.Millisecond)
	_ = conn.Close()
	select {
	case e := <-errCh:
		if e == nil {
			t.Fatal("expected close error on blocked read")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("blocked read was not unblocked by close")
	}
}

func TestWriteDeadlineBehavior(t *testing.T) {
	left, err := NewWithConfig(Config{Delay: 0, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := NewWithConfig(Config{Delay: 0, Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	if err := left.SetWriteDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := left.WriteTo([]byte{1}, right.LocalAddr()); err == nil {
		t.Fatal("expected write timeout with expired deadline")
	}

	if err := left.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := left.WriteTo([]byte{1}, right.LocalAddr()); err != nil {
		t.Fatalf("expected successful write after deadline reset, got %v", err)
	}
}

func TestDelayDeviationAffectsObservedDelay(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:          5 * time.Millisecond,
		DelayDeviation: 0,
		RandomLossRate: 0,
		QueueSize:      256,
		Seed:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()

	right, err := NewWithConfig(Config{Delay: 0, QueueSize: 256, Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	buf := make([]byte, 8)

	for i := 0; i < 20; i++ {
		if _, err := left.WriteTo([]byte("a"), right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		if err := right.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := right.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}
	zeroDeviationMax := right.Stats().MaxDelay

	right.ResetStats()
	left.SetDelayDeviation(10)
	for i := 0; i < 40; i++ {
		if _, err := left.WriteTo([]byte("b"), right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		if err := right.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := right.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}
	highDeviationMax := right.Stats().MaxDelay
	if highDeviationMax <= zeroDeviationMax {
		t.Fatalf("expected higher max delay with larger deviation, baseline=%v high=%v", zeroDeviationMax, highDeviationMax)
	}
}

func TestBandwidthShapingThroughputBounds(t *testing.T) {
	const (
		payloadSize = 1024
		packets     = 24
		rateBps     = 24 * 1024
	)
	left, err := NewWithConfig(Config{
		Delay:                0,
		DelayDeviation:       0,
		BandwidthBytesPerSec: rateBps,
		QueueSize:            2048,
		Seed:                 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := NewWithConfig(Config{Delay: 0, QueueSize: 2048, Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	start := time.Now()
	for i := 0; i < packets; i++ {
		if _, err := left.WriteTo(make([]byte, payloadSize), right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}

	buf := make([]byte, payloadSize)
	for i := 0; i < packets; i++ {
		if err := right.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := right.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	expected := time.Duration((packets*payloadSize)*int(time.Second)/rateBps) - 80*time.Millisecond
	if expected < 0 {
		expected = 0
	}
	if elapsed < expected {
		t.Fatalf("throughput too high, shaping likely bypassed: elapsed=%v expected-min=%v", elapsed, expected)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("throughput too low or stalled: %v", elapsed)
	}
}

func TestLongRunCounterConsistency(t *testing.T) {
	const totalPackets = 400

	left, err := NewWithConfig(Config{
		Delay:          time.Millisecond,
		RandomLossRate: 0.1,
		BurstLossRate:  0.03,
		BurstLength:    2,
		QueueSize:      4096,
		Seed:           123,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer left.Close()

	right, err := NewWithConfig(Config{
		Delay:     0,
		QueueSize: 4096,
		Seed:      456,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	for i := 0; i < totalPackets; i++ {
		if _, err := left.WriteTo([]byte{byte(i)}, right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}

	lst := left.Stats()
	if lst.SentPackets != totalPackets {
		t.Fatalf("unexpected sent count: got=%d want=%d", lst.SentPackets, totalPackets)
	}
	dropped := lst.DropRandom + lst.DropBurst
	wantDelivered := uint64(totalPackets) - dropped
	deadline := time.Now().Add(2 * time.Second)
	for {
		rst := right.Stats()
		if rst.RecvPackets+rst.DropQueue == wantDelivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery counters did not settle: want=%d left=%+v right=%+v", wantDelivered, lst, rst)
		}
		time.Sleep(time.Millisecond)
	}

	rst := right.Stats()
	delivered := rst.RecvPackets + rst.DropQueue
	if delivered+dropped != totalPackets {
		t.Fatalf("counter mismatch: delivered=%d dropped=%d total=%d left=%+v right=%+v", delivered, dropped, totalPackets, lst, rst)
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	for _, size := range []int{64, 1024, 65536} {
		b.Run(fmt.Sprintf("payload-%d", size), func(b *testing.B) {
			left, err := NewWithConfig(Config{DelayDeviation: 0, QueueSize: 64, Seed: 1})
			if err != nil {
				b.Fatal(err)
			}
			defer left.Close()
			right, err := NewWithConfig(Config{QueueSize: 64, Seed: 2})
			if err != nil {
				b.Fatal(err)
			}
			defer right.Close()

			payload := make([]byte, size)
			buf := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := left.WriteTo(payload, right.LocalAddr()); err != nil {
					b.Fatal(err)
				}
				if _, _, err := right.ReadFrom(buf); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestSimulationMatrix(t *testing.T) {
	type scenario struct {
		name          string
		left          Config
		right         Config
		packets       int
		payloadSize   int
		readAfterSend bool
		verify        func(t *testing.T, sent int, elapsed time.Duration, left Stats, right Stats)
	}

	run := func(t *testing.T, sc scenario) (time.Duration, Stats, Stats) {
		t.Helper()

		left, err := NewWithConfig(sc.left)
		if err != nil {
			t.Fatal(err)
		}
		defer left.Close()

		right, err := NewWithConfig(sc.right)
		if err != nil {
			t.Fatal(err)
		}
		defer right.Close()

		start := time.Now()
		for i := 0; i < sc.packets; i++ {
			if _, err := left.WriteTo(make([]byte, sc.payloadSize), right.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			if !sc.readAfterSend {
				continue
			}
			if err := right.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, sc.payloadSize)
			if _, _, err := right.ReadFrom(buf); err != nil {
				t.Fatal(err)
			}
		}

		if sc.readAfterSend {
			return time.Since(start), left.Stats(), right.Stats()
		}

		buf := make([]byte, sc.payloadSize)
		for {
			if err := right.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := right.ReadFrom(buf); err != nil {
				if isTimeout(err) {
					break
				}
				t.Fatal(err)
			}
		}
		return time.Since(start), left.Stats(), right.Stats()
	}

	matrix := []scenario{
		{
			name: "clean-link",
			left: Config{
				Delay:          0,
				DelayDeviation: 0,
				QueueSize:      256,
				Seed:           1,
			},
			right:         Config{QueueSize: 256, Seed: 2},
			packets:       64,
			payloadSize:   256,
			readAfterSend: true,
			verify: func(t *testing.T, sent int, _ time.Duration, left Stats, right Stats) {
				if left.DropRandom != 0 || left.DropBurst != 0 || right.DropQueue != 0 {
					t.Fatalf("clean-link should not drop packets, left=%+v right=%+v", left, right)
				}
				if int(right.RecvPackets) != sent {
					t.Fatalf("clean-link recv mismatch: got=%d want=%d", right.RecvPackets, sent)
				}
			},
		},
		{
			name: "random-loss",
			left: Config{
				Delay:          0,
				DelayDeviation: 0,
				RandomLossRate: 0.25,
				QueueSize:      1024,
				Seed:           11,
			},
			right:         Config{QueueSize: 1024, Seed: 12},
			packets:       240,
			payloadSize:   128,
			readAfterSend: false,
			verify: func(t *testing.T, sent int, _ time.Duration, left Stats, right Stats) {
				if left.DropRandom == 0 {
					t.Fatalf("expected random loss > 0, left=%+v", left)
				}
				if right.RecvPackets >= uint64(sent) {
					t.Fatalf("random-loss expected recv < sent, recv=%d sent=%d", right.RecvPackets, sent)
				}
			},
		},
		{
			name: "burst-loss",
			left: Config{
				Delay:          0,
				DelayDeviation: 0,
				BurstLossRate:  0.05,
				BurstLength:    4,
				QueueSize:      1024,
				Seed:           21,
			},
			right:         Config{QueueSize: 1024, Seed: 22},
			packets:       300,
			payloadSize:   128,
			readAfterSend: false,
			verify: func(t *testing.T, sent int, _ time.Duration, left Stats, right Stats) {
				if left.DropBurst == 0 {
					t.Fatalf("expected burst loss > 0, left=%+v", left)
				}
				if right.RecvPackets >= uint64(sent) {
					t.Fatalf("burst-loss expected recv < sent, recv=%d sent=%d", right.RecvPackets, sent)
				}
			},
		},
		{
			name: "reorder",
			left: Config{
				Delay:           0,
				DelayDeviation:  0,
				ReorderRate:     1,
				MaxReorderDelay: 10 * time.Millisecond,
				QueueSize:       1024,
				Seed:            31,
			},
			right:         Config{QueueSize: 1024, Seed: 32},
			packets:       120,
			payloadSize:   64,
			readAfterSend: false,
			verify: func(t *testing.T, sent int, _ time.Duration, left Stats, right Stats) {
				if left.Reordered == 0 {
					t.Fatalf("expected reordered > 0, left=%+v", left)
				}
				if int(right.RecvPackets)+int(left.DropRandom+left.DropBurst) != sent {
					t.Fatalf("reorder conservation mismatch: left=%+v right=%+v sent=%d", left, right, sent)
				}
			},
		},
		{
			name: "bandwidth-shaped",
			left: Config{
				Delay:                0,
				DelayDeviation:       0,
				BandwidthBytesPerSec: 16 * 1024,
				QueueSize:            2048,
				Seed:                 41,
			},
			right:         Config{QueueSize: 2048, Seed: 42},
			packets:       20,
			payloadSize:   1024,
			readAfterSend: true,
			verify: func(t *testing.T, sent int, elapsed time.Duration, left Stats, right Stats) {
				if int(right.RecvPackets) != sent {
					t.Fatalf("bandwidth-shaped recv mismatch: got=%d want=%d", right.RecvPackets, sent)
				}
				minExpected := 900 * time.Millisecond
				if elapsed < minExpected {
					t.Fatalf("bandwidth shaping appears bypassed: elapsed=%v min=%v left=%+v", elapsed, minExpected, left)
				}
			},
		},
		{
			name: "queue-pressure",
			left: Config{
				Delay:          0,
				DelayDeviation: 0,
				QueueSize:      512,
				Seed:           51,
			},
			right: Config{
				Delay:     0,
				QueueSize: 1,
				Seed:      52,
			},
			packets:       100,
			payloadSize:   64,
			readAfterSend: false,
			verify: func(t *testing.T, sent int, _ time.Duration, left Stats, right Stats) {
				if right.DropQueue == 0 {
					t.Fatalf("expected queue drops > 0, right=%+v", right)
				}
				if int(right.RecvPackets+right.DropQueue)+int(left.DropRandom+left.DropBurst) != sent {
					t.Fatalf("queue-pressure conservation mismatch: left=%+v right=%+v sent=%d", left, right, sent)
				}
			},
		},
	}

	for _, sc := range matrix {
		t.Run(sc.name, func(t *testing.T) {
			elapsed, leftStats, rightStats := run(t, sc)
			sc.verify(t, sc.packets, elapsed, leftStats, rightStats)
		})
	}
}

type fakeAddr string

func isTimeout(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

func (a fakeAddr) Network() string { return "lossy" }
func (a fakeAddr) String() string  { return string(a) }

var _ net.PacketConn = (*LossyConn)(nil)
