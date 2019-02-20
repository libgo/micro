package micro

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// go build -ldflags -X
var gitCommit, buildDate string

// VersionInfo return build version info
func VersionInfo() string {
	return gitCommit + ", " + buildDate
}

type Micro interface {
	AddCloseFunc(f func() error) // exec when Close func is called
	ServeGRPC(bindAddr string, server GRPCServer)
	ServeHTTP(bindAddr string, handler http.Handler)
	Start()
}

type micro struct {
	name       string
	closeFuncs []func() error
	errChan    chan error
	serveFuncs []func()
}

// New create Micro, serviceName.0 is service name.
func New(serviceName ...string) Micro {
	m := &micro{
		errChan:    make(chan error, 1),
		closeFuncs: make([]func() error, 0),
		serveFuncs: make([]func(), 0),
	}

	if len(serviceName) != 0 {
		m.name = serviceName[0]
	}

	return m
}

// AddResCloseFunc add resource close func
func (m *micro) AddCloseFunc(f func() error) {
	m.closeFuncs = append(m.closeFuncs, f)
}

func (m *micro) createListener(bindAddr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, err
	}

	m.AddCloseFunc(func() error {
		err := ln.Close()
		// skip ln already closed error
		if e, ok := err.(*net.OpError); ok && e.Op == "close" {
			return nil
		}
		return err
	})

	return ln, nil
}

// GRPCServer
type GRPCServer interface {
	Serve(net.Listener) error
	GracefulStop()
}

// ServeGRPC is helper func to start gRPC server
func (m *micro) ServeGRPC(bindAddr string, server GRPCServer) {
	m.serveFuncs = append(m.serveFuncs, func() {
		ln, err := m.createListener(bindAddr)
		if err != nil {
			m.errChan <- err
			return
		}

		m.AddCloseFunc(func() error {
			server.GracefulStop()
			return nil
		})

		err = server.Serve(ln)
		if err != nil {
			m.errChan <- err
		}
	})
}

// TODO other params can optimize
func (m *micro) ServeHTTP(bindAddr string, handler http.Handler) {
	m.serveFuncs = append(m.serveFuncs, func() {
		ln, err := m.createListener(bindAddr)
		if err != nil {
			m.errChan <- err
			return
		}

		server := &http.Server{
			Handler: handler,
		}

		m.AddCloseFunc(func() error {
			ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*30)
			defer cancelFunc()
			return server.Shutdown(ctx)
		})

		err = server.Serve(ln)
		if err != nil {
			m.errChan <- err
		}
	})
}

// close all added resource FILO
func (m *micro) close() {
	for i := len(m.closeFuncs) - 1; i >= 0; i-- {
		err := m.closeFuncs[i]()
		if err != nil {
			log.Println(err)
		}
	}
}

// WatchSignal notify signal to stop running
var WatchSignal = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGKILL, syscall.SIGHUP, syscall.SIGQUIT}

// Wait util signal
func (m *micro) Start() {
	var err error

	defer func() {
		m.close()
		if err != nil {
			log.Fatalf("micro %q receive err signal: %s\n", m.name, err)
		}
	}()

	for i := range m.serveFuncs {
		go m.serveFuncs[i]()
	}

	log.Println("micro " + m.name + " start")

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, WatchSignal...)
	select {
	case s := <-ch:
		log.Printf("micro %q receive stop signal: %s\n", m.name, s)
	case err = <-m.errChan:
	}
}

// Bind is a helper func to read env port and returns bind addr
func Bind(addr string, envName ...string) string {
	env := "bind_addr"
	if len(envName) != 0 {
		env = envName[0]
	}

	if p := os.Getenv(env); p != "" {
		addr = p
	}

	// :port, it panics if port is empty
	if addr[0] == ':' {
		return addr
	}

	// port only, yes, it may return :0, caution
	if _, err := strconv.Atoi(addr); err == nil {
		return ":" + addr
	}

	return addr
}
