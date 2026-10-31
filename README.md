# lossyconn

[![GoDoc][1]][2] [![MIT licensed][3]][4] [![Coverage Status][5]][6]

[1]: https://godoc.org/github.com/xtaci/lossyconn?status.svg
[2]: https://godoc.org/github.com/xtaci/lossyconn
[3]: https://img.shields.io/badge/license-MIT-blue.svg
[4]: LICENSE
[5]: https://codecov.io/gh/xtaci/lossyconn/branch/master/graph/badge.svg
[6]: https://codecov.io/gh/xtaci/lossyconn

`lossyconn` is an in-memory, packet-oriented network simulator for Go tests. It
implements `net.PacketConn` and can simulate one-way delay, delay variation,
bandwidth limits, random and burst packet loss, packet reordering, and bounded
queues. A fixed seed makes a simulation reproducible.

## Quick start

Create two simulated endpoints and send a datagram:

```go
left, err := lossyconn.NewLossyConn(0.3, 200) // 30% loss, 200 ms one-way delay
if err != nil {
	return err
}
defer left.Close()

right, err := lossyconn.NewLossyConn(0.2, 180)
if err != nil {
	return err
}
defer right.Close()

payload := []byte("hello")
if _, err := left.WriteTo(payload, right.LocalAddr()); err != nil {
	return err
}

buf := make([]byte, 1024)
if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
	return err
}
n, from, err := right.ReadFrom(buf)
if err != nil {
	return err
}
fmt.Printf("received %q from %v\n", buf[:n], from)
```

Import `fmt`, `time`, and `github.com/xtaci/lossyconn` in the example's package.
`NewLossyConn` keeps the legacy constructor signature; use `NewWithConfig` for
the additional simulation options.

## Configure a link

```go
conn, err := lossyconn.NewWithConfig(lossyconn.Config{
	Delay:                50 * time.Millisecond,
	DelayDeviation:       2, // standard deviation, in milliseconds
	BandwidthBytesPerSec: 2 * 1024 * 1024,
	RandomLossRate:       0.01,
	BurstLossRate:        0.002,
	BurstLength:          3,
	ReorderRate:          0.02,
	MaxReorderDelay:      8 * time.Millisecond,
	QueueSize:            4096,
	MaxPendingPackets:    4096,
	Seed:                 42,
})
if err != nil {
	return err
}
defer conn.Close()
```

`Config` fields:

- `Delay`: base one-way delay.
- `DelayDeviation`: standard deviation of normally distributed delay variation,
  in milliseconds.
- `BandwidthBytesPerSec`: maximum outgoing bandwidth; zero disables shaping.
- `RandomLossRate`: independent packet-loss probability from 0 to 1.
- `BurstLossRate` and `BurstLength`: probability and length of consecutive
  packet-loss bursts.
- `ReorderRate` and `MaxReorderDelay`: probability and maximum extra delay for
  reordering.
- `QueueSize`: maximum number of received packets buffered per connection.
- `MaxPendingPackets`: maximum number of outgoing packets waiting for delivery.
  When this queue is full, `WriteTo` returns an error. Zero selects the default
  limit of 4096 packets for both queue limits.
- `Seed`: deterministic random seed; zero selects a time-based seed.

Simulated packet loss is reported as a successful `WriteTo`, matching the
behavior of sending a datagram over a lossy network. Queue overflow, unreachable
destinations, and expired write deadlines return errors. Read deadlines return
an error that implements `net.Error`.

## Observe simulation statistics

`Stats` returns a per-connection snapshot. `ResetStats` clears the counters:

```go
stats := conn.Stats()
fmt.Printf("sent=%d received=%d random drops=%d receive queue drops=%d send queue drops=%d\n",
	stats.SentPackets, stats.RecvPackets, stats.DropRandom, stats.DropQueue,
	stats.DropPendingQueue)
conn.ResetStats()
```

`NewLossyPacketConn(loss, delayMs)` is retained as a deprecated alias of
`NewLossyConn`.
