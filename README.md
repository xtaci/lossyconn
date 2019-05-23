# lossyconn
lossy packet connection simulator

```
	left, err := NewLossyPacketConn(0.3, 200)
	if err != nil {
		t.Fatal(err)
	}

	right, err := NewLossyPacketConn(0.2, 180)
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
