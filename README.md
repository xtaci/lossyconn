# lossyconn

[![GoDoc][1]][2] [![MIT licensed][3]][4] [![Coverage Status][5]][6] [![Created at][7]][8]

[1]: https://godoc.org/github.com/xtaci/lossyconn?status.svg
[2]: https://godoc.org/github.com/xtaci/lossyconn
[3]: https://img.shields.io/badge/license-MIT-blue.svg
[4]: LICENSE
[5]: https://codecov.io/gh/xtaci/lossyconn/branch/master/graph/badge.svg
[6]: https://codecov.io/gh/xtaci/lossyconn
[7]: https://img.shields.io/github/created-at/xtaci/lossyconn
[8]: https://github.com/xtaci/lossyconn

`lossyconn` is an in-memory, packet-oriented network simulator for Go tests. It
implements `net.PacketConn` and can simulate one-way delay, delay variation,
bandwidth limits, congestion queueing, link outages, maximum packet sizes and
lifetimes, random and burst packet loss, packet duplication and payload
corruption, packet reordering, and bounded queues. A fixed seed makes a
simulation reproducible.

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
	MaxPacketSize:        1200,
	PacketLifetime:       2 * time.Second,
	QueueDelayPerPacket:  2 * time.Millisecond,
	MaxQueueDelay:        100 * time.Millisecond,
	RandomLossRate:       0.01,
	BurstLossRate:        0.002,
	BurstLength:          3,
	DuplicateRate:        0.001,
	CorruptionRate:       0.0001,
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
- `MaxPacketSize`: maximum outgoing payload size in bytes; zero disables the
  limit. Larger packets are counted as sent but dropped before delivery.
- `PacketLifetime`: maximum time a packet may remain in transit; zero disables
  expiration. Expired packets are discarded and counted by the receiving
  connection.
- `QueueDelayPerPacket` and `MaxQueueDelay`: add delay based on the number of
  packets already waiting in the outgoing queue, capped at `MaxQueueDelay`.
  `MaxQueueDelay` must be positive when queue delay is enabled. Both values are
  zero by default.
- `RandomLossRate`: independent packet-loss probability from 0 to 1.
- `BurstLossRate` and `BurstLength`: probability and length of consecutive
  packet-loss bursts.
- `DuplicateRate`: probability of scheduling one additional copy of an outgoing
  packet. Both copies are accepted or rejected together by the bounded send
  queue.
- `CorruptionRate`: probability of flipping one random payload bit in an
  outgoing packet. Empty payloads are not corrupted.
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

Use `SetLinkState(false)` to simulate an outgoing link outage and
`SetLinkState(true)` to restore it. Writes during an outage still succeed, but
the packets are discarded and counted in `DropLinkDown`.

## Configure links per peer

Each destination can have independent delay, bandwidth, loss, queue shaping,
and link state. The profile's `Config` uses the same link settings documented
above; receive/send queue limits and random seed remain connection-wide:

```go
if err := left.SetPeerProfile(right.LocalAddr(), lossyconn.LinkProfile{
	Config: lossyconn.Config{
		Delay:          80 * time.Millisecond,
		DelayDeviation: 2,
		RandomLossRate: 0.02,
	},
	Up: true,
}); err != nil {
	return err
}
```

`SchedulePeerProfile` adds a complete profile that takes effect for writes
started at or after a specified time. Schedule multiple profiles to model
changing conditions such as congestion followed by recovery; each scheduled
profile replaces, rather than merges with, the previous one. Schedule another
profile with `Up: true` to restore a link after a simulated outage.
`SetPeerLinkState` changes one peer's state immediately, and
`ClearPeerProfile` returns it to the connection's default profile. Statistics
are aggregated per connection.

## Observe simulation statistics

`Stats` returns a per-connection snapshot. Receive-side counters include queue
and expired-packet drops. `ResetStats` clears the counters:

```go
stats := conn.Stats()
fmt.Printf("sent=%d received=%d duplicated=%d corrupted=%d random drops=%d oversized drops=%d expired drops=%d link-down drops=%d queue-delayed=%d receive queue drops=%d send queue drops=%d\n",
	stats.SentPackets, stats.RecvPackets, stats.DuplicatedPackets, stats.CorruptedPackets,
	stats.DropRandom, stats.DropOversize, stats.DropExpired, stats.DropLinkDown,
	stats.QueueDelayedPackets, stats.DropQueue, stats.DropPendingQueue)
conn.ResetStats()
```

`NewLossyPacketConn(loss, delayMs)` is retained as a deprecated alias of
`NewLossyConn`.
