package micro

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// go build -ldflags -X
var gitCommit, buildDate string

// VersionInfo return build version info
func VersionInfo() string {
	return gitCommit + ", " + buildDate
}

type Logger interface {
	SetAttach(map[string]interface{}) // for log ctx
	Info(string)
	Error(interface{})
}

var _ Logger = &defaultLogger{}

type defaultLogger struct {
	l *log.Logger
}

func (d *defaultLogger) SetAttach(kv map[string]interface{}) {
	for k, v := range kv {
		d.l.SetPrefix(k + "=" + fmt.Sprint(v) + " ")
	}
}

func (d *defaultLogger) Info(str string) {
	d.l.Println(str)
}

func (d *defaultLogger) Error(v interface{}) {
	d.l.Println(v)
}

type Micro interface {
	WithLogger(Logger)
	AddCloseFunc(f ...func() error) // exec when Close func is called
	ServeGRPC(bindAddr string, server GRPCServer)
	ServeHTTP(bindAddr string, handler http.Handler)
	Start()
}

type micro struct {
	mu         sync.RWMutex
	name       string
	logger     Logger
	closeFuncs []func() error
	errChan    chan error
	serveFuncs []func()
}

// New create Micro, svcName.0 use as service name.
func New(svcName ...string) Micro {
	m := &micro{
		logger:     &defaultLogger{l: log.New(os.Stdout, "", log.LstdFlags)},
		errChan:    make(chan error, 1),
		closeFuncs: make([]func() error, 0),
		serveFuncs: make([]func(), 0),
	}

	if len(svcName) != 0 {
		m.name = svcName[0]
		m.WithLogger(m.logger)
	}

	return m
}

// WithLogger set micro logger, it add svc and ver if not empty.
func (m *micro) WithLogger(l Logger) {
	if m.name != "" {
		l.SetAttach(map[string]interface{}{
			"svc": m.name,
		})
	}

	if gitCommit != "" {
		l.SetAttach(map[string]interface{}{
			"ver": gitCommit,
		})
	}

	// log should close last, prepend to m.closeFuncs

	// if Logger impl Close() error
	if li, ok := l.(interface {
		Close() error
	}); ok {
		m.closeFuncs = append([]func() error{li.Close}, m.closeFuncs...)
	}

	// if Logger impl Close()
	if li, ok := l.(interface {
		Close()
	}); ok {
		m.closeFuncs = append([]func() error{func() error {
			li.Close()
			return nil
		}}, m.closeFuncs...)
	}

	m.logger = l
}

// AddCloseFunc add resource close func, used for resource release FILO when exit.
func (m *micro) AddCloseFunc(f ...func() error) {
	m.mu.Lock()
	m.closeFuncs = append(m.closeFuncs, f...)
	m.mu.Unlock()
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

// using toupper(name(- to _))_BIND_[SUFFIX?] > MICRO_BIND[_SUFFIX?] > default
func (m *micro) genBind(addr string) string {
	suffix := ""

	if s := strings.Split(addr, "|"); len(s) == 2 {
		suffix, addr = strings.ToUpper(s[0]), s[1]
		if suffix != "" {
			suffix = "_" + suffix
		}
	}

	// replace and to upper
	if s := os.Getenv(strings.ToUpper(strings.Replace(m.name, "-", "_", -1)) + "_BIND" + suffix); s != "" {
		addr = s
	} else if s := os.Getenv("MICRO_BIND" + suffix); s != "" {
		addr = s
	}

	// adding : as prefix if not exist
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	return addr
}

// ServeGRPC is helper func to start gRPC server
func (m *micro) ServeGRPC(addr string, server GRPCServer) {
	m.serveFuncs = append(m.serveFuncs, func() {
		ln, err := m.createListener(m.genBind(addr))
		if err != nil {
			m.errChan <- err
			return
		}

		m.AddCloseFunc(func() error {
			stopSig := make(chan struct{})
			go func() {
				server.GracefulStop()
				close(stopSig)
			}()

			select {
			case <-stopSig:
			case <-time.After(time.Second * 45):
				if m.logger != nil {
					m.logger.Info("graceful max exceed, force stop")
				}
			}

			return nil
		})

		err = server.Serve(ln)
		if err != nil {
			m.errChan <- err
		}
	})
}

// ServeHTTP is helper func to start HTTP server
// TODO other params can optimize
func (m *micro) ServeHTTP(addr string, handler http.Handler) {
	m.serveFuncs = append(m.serveFuncs, func() {
		ln, err := m.createListener(m.genBind(addr))
		if err != nil {
			m.errChan <- err
			return
		}

		server := &http.Server{
			Handler: handler,
		}

		m.AddCloseFunc(func() error {
			ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*45)
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
	m.mu.RLock()
	for i := len(m.closeFuncs) - 1; i >= 0; i-- {
		err := m.closeFuncs[i]()
		if err != nil && m.logger != nil {
			m.logger.Info(err.Error())
		}
	}
	m.mu.RLock()
}

// WatchSignal notify signal to stop running
var WatchSignal = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGKILL, syscall.SIGHUP, syscall.SIGQUIT}

// Wait util signal
func (m *micro) Start() {
	var err error

	defer func() {
		if err != nil && m.logger != nil {
			m.logger.Error(fmt.Sprintf("micro receive err signal: %s\n", err))
		}
		m.close()
	}()

	for i := range m.serveFuncs {
		go m.serveFuncs[i]()
	}

	if m.logger != nil {
		m.logger.Info("micro start")
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, WatchSignal...)
	select {
	case s := <-ch:
		if m.logger != nil {
			m.logger.Info(fmt.Sprintf("micro receive stop signal: %s\n", s))
		}
	case err = <-m.errChan:
	}
}
