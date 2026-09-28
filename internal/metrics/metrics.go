package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	TransactionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ledger_transactions_total",
			Help: "Total count of processed transactions partitioned by kind and status",
		},
		[]string{"kind", "status"},
	)

	TransactionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ledger_transaction_duration_seconds",
			Help:    "Latency histogram of transaction processing in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		},
		[]string{"kind"},
	)

	ActiveHoldsCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ledger_active_holds_count",
			Help: "Current count of active authorization holds across all accounts",
		},
	)

	ReconciliationStatus = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ledger_reconciliation_status",
			Help: "Current global ledger integrity status (1 = healthy, 0 = discrepancy)",
		},
	)
)
