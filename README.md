Example:
```
package main

import (
    "auth"

	"github.com/libgo/logx"
	"github.com/libgo/micro"
    "github.com/libgo/grpcx"
)

func main() {
	m := micro.New("hello-world")
	m.WithLogger(logx.Logger())

    // resource release when svc stop
	m.AddCloseFunc(logx.Close)

	s := grpcx.NewServer()
	auth.RegisterXXXXServiceServer(s, &XXXXService{})

    // will try read env HELLO_WORLD_BIND > MICRO_BIND > :9090
	m.ServeGRPC(":9090", s)

    // this will try read env HELLO_WORLD_BINDOTHER > MICRO_BINDOTHER > :9091
    m.ServeGRPC("OTHER|:9091", s)

	m.Start()
}
```