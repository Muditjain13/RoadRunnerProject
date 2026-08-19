package service

import "github.com/prometheus/client_golang/prometheus"

// Metrics is notification's RED-adjacent view: since it's a Kafka
// consumer, not a request/response service, "rate" and "errors" are
// tracked per processing outcome rather than per endpoint.
type Metrics struct {
	messagesTotal *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		messagesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_messages_total",
			Help: "Total booking events processed, labeled by outcome.",
		}, []string{"outcome"}),
	}
	reg.MustRegister(m.messagesTotal)
	return m
}

const (
	outcomeNotified     = "notified"
	outcomeDuplicate    = "duplicate"
	outcomePoison       = "poison"
	outcomeNotifyFailed = "notify_failed"
)
