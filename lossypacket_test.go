package lossyconn

import (
	"errors"
	"net"
	"sync"
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
}

func TestNewLossyPacketConnCompatibility(t *testing.T) {
	left, err := NewLossyPacketConn(0.1, 10)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewLossyPacketConn(0.1, 10)
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

func TestBandwidthAndReorderStats(t *testing.T) {
	left, err := NewWithConfig(Config{
		Delay:               0,
		DelayDeviation:      0,
		BandwidthBytesPerSec: 100,
		ReorderRate:         1,
		MaxReorderDelay:     5 * time.Millisecond,
		Seed:                1,
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
		Delay:           5 * time.Millisecond,
		DelayDeviation:  0,
		RandomLossRate:  0,
		QueueSize:       256,
		Seed:            1,
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

	var wg sync.WaitGroup
	readErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8)
		for {
			if err := right.SetReadDeadline(time.Now().Add(120 * time.Millisecond)); err != nil {
				readErr <- err
				return
			}
			if _, _, err := right.ReadFrom(buf); err != nil {
				if err.Error() == "i/o timeout" {
					return
				}
				readErr <- err
				return
			}
		}
	}()

	for i := 0; i < totalPackets; i++ {
		if _, err := left.WriteTo([]byte{byte(i)}, right.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(readErr)
	for err := range readErr {
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	}

	lst := left.Stats()
	rst := right.Stats()
	if lst.SentPackets != totalPackets {
		t.Fatalf("unexpected sent count: got=%d want=%d", lst.SentPackets, totalPackets)
	}
	delivered := rst.RecvPackets + rst.DropQueue
	dropped := lst.DropRandom + lst.DropBurst
	if delivered+dropped != totalPackets {
		t.Fatalf("counter mismatch: delivered=%d dropped=%d total=%d left=%+v right=%+v", delivered, dropped, totalPackets, lst, rst)
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
				if err.Error() == "i/o timeout" {
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
			right: Config{QueueSize: 256, Seed: 2},
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

func (a fakeAddr) Network() string { return "lossy" }
func (a fakeAddr) String() string  { return string(a) }

var _ net.PacketConn = (*LossyConn)(nil)
