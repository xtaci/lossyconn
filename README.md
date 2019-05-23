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

Package lossyconn is a lossy connection simulator for Golang.

lossyconn provides packet oriented lossy connection for testing purpose

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
