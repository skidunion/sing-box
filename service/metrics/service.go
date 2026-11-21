package metrics

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/listener"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func RegisterService(registry *boxService.Registry) {
	boxService.Register[option.ListenOptions](registry, C.TypeMetrics, NewService)
}

type Service struct {
	boxService.Adapter
	logger   log.ContextLogger
	listener *listener.Listener
	registry *prometheus.Registry
}

func NewService(ctx context.Context, logger log.ContextLogger, tag string, options option.ListenOptions) (adapter.Service, error) {
	if options.ListenPort == 0 {
		return nil, E.New("metrics service requires listen_port")
	}
	return &Service{
		Adapter: boxService.NewAdapter(C.TypeMetrics, tag),
		logger:  logger,
		listener: listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Network: []string{N.NetworkTCP},
			Listen:  options,
		}),
		registry: prometheus.NewRegistry(),
	}, nil
}

func (s *Service) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStarted {
		return nil
	}
	tcpListener, err := s.listener.ListenTCP()
	if err != nil {
		return err
	}

	scope.Add(s.listener.Close)
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	ctx := scope.Context()
	server := &http.Server{
		Handler:           mux,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	scope.Add(server.Close)
	go func() {
		if err := server.Serve(tcpListener); err != nil && ctx.Err() == nil {
			s.logger.Error("serve metrics: ", err)
		}
	}()
	return nil
}

// Register exposes a component's collector on the configured metrics services
// for the lifetime of the component. Call it from the component's Start method
// once. A box without a metrics service does not collect the metrics.
func Register(scope *adapter.Scope, collector prometheus.Collector) error {
	manager := service.FromContext[adapter.ServiceManager](scope.Context())
	if manager == nil {
		return nil
	}
	for _, configured := range manager.Services() {
		metricsService, loaded := configured.(*Service)
		if !loaded {
			continue
		}
		if err := metricsService.registry.Register(collector); err != nil {
			return E.Cause(err, "register collector with metrics service ", configured.Tag())
		}
		scope.Add(func() error {
			metricsService.registry.Unregister(collector)
			return nil
		})
	}
	return nil
}
