# Lossy connection simulator

[![GoDoc][1]][2] [![MIT licensed][11]][12] [![Build Status][3]][4] [![Go Report Card][5]][6] [![Coverage Statusd][7]][8]

[1]: https://godoc.org/github.com/xtaci/lossyconn?status.svg
[2]: https://godoc.org/github.com/xtaci/lossyconn
[3]: https://travis-ci.org/xtaci/lossyconn.svg?branch=master
[4]: https://travis-ci.org/xtaci/lossyconn
[5]: https://goreportcard.com/badge/github.com/xtaci/lossyconn
[6]: https://goreportcard.com/report/github.com/xtaci/lossyconn
[7]: https://codecov.io/gh/xtaci/lossyconn/branch/master/graph/badge.svg
[8]: https://codecov.io/gh/xtaci/lossyconn
[11]: https://img.shields.io/badge/license-MIT-blue.svg
[12]: LICENSE

Package lossyconn is a packet-oriented link simulator for Golang.
`NewLossyConn(loss, delayMs)` remains backward-compatible for existing users (including kcp-go tests).

```
	left, err := NewLossyConn(0.3, 200)
	if err != nil {
		t.Fatal(err)
	}

	right, err := NewLossyConn(0.2, 180)
	if err != nil {
		t.Fatal(err)
	}

	p := make([]byte, 1024)
	left.WriteTo(p, right.LocalAddr())
	right.ReadFrom(p)

	left.Close()
	right.Close()
	t.Logf("left:%v\n", left)
	t.Logf("right:%v\n", right)
```

Advanced simulation can be configured with `NewWithConfig`:

```go
conn, _ := NewWithConfig(Config{
	Delay:                50 * time.Millisecond,
	DelayDeviation:       2,
	BandwidthBytesPerSec: 2 * 1024 * 1024,
	RandomLossRate:       0.01,
	BurstLossRate:        0.002,
	BurstLength:          3,
	ReorderRate:          0.02,
	MaxReorderDelay:      8 * time.Millisecond,
	Seed:                 42,
})
_ = conn
```

`Config` supports:

- one-way delay (`Delay`) and delay variation (`DelayDeviation`)
- bandwidth shaping (`BandwidthBytesPerSec`)
- random loss (`RandomLossRate`) and burst loss (`BurstLossRate`, `BurstLength`)
- reordering (`ReorderRate`, `MaxReorderDelay`)
- receive queue limit (`QueueSize`)
- deterministic simulation seed (`Seed`)

Per-connection counters are available via `Stats()` and can be cleared by `ResetStats()`.

Backward-compatibility alias:

```go
conn, _ := NewLossyPacketConn(0.1, 100) // deprecated alias of NewLossyConn
_ = conn
```
