package database

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestCollectorNilManagerReturnsNil(t *testing.T) {
	var m *DBManager
	if c := m.Collector(); c != nil {
		t.Fatal("nil manager Collector() should return nil")
	}
}

func TestCollectorEmptyManagerReturnsNil(t *testing.T) {
	m := &DBManager{}
	if c := m.Collector(); c != nil {
		t.Fatal("manager without pool Collector() should return nil")
	}
}

func TestCollectorDescribeEmitsAllDescs(t *testing.T) {
	m, err := Init(t.Context(), Config{DSN: unreachableDSN})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	c := m.Collector()
	if c == nil {
		t.Fatal("expected non-nil collector for configured pool")
	}

	ch := make(chan *prometheus.Desc, 16)
	c.Describe(ch)
	close(ch)

	var count int
	for range ch {
		count++
	}
	if count != 8 {
		t.Fatalf("Describe emitted %d descs, want 8", count)
	}
}

// TestCollectorCollectReadsPoolStat 用惰性 pool（unreachableDSN 未真正拨号）
// 断言 Collect 顺序与取值：MaxConns 应反映配置值，其余累计型指标在没有真实
// acquire 发生时应为 0——这足以验证 Stat() 字段到 Desc 的映射没接错线，不需
// 要真实数据库。
func TestCollectorCollectReadsPoolStat(t *testing.T) {
	m, err := Init(t.Context(), Config{DSN: unreachableDSN, MaxConns: 9})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	c := m.Collector()
	ch := make(chan prometheus.Metric, 16)
	c.Collect(ch)
	close(ch)

	var metrics []prometheus.Metric
	for metric := range ch {
		metrics = append(metrics, metric)
	}
	if len(metrics) != 8 {
		t.Fatalf("Collect emitted %d metrics, want 8", len(metrics))
	}

	// 顺序固定为 Collect 里写死的：acquired/idle/total/max (gauge) 后接
	// acquire_count/empty_acquire/canceled_acquire/acquire_duration (counter)。
	wantGauge := []float64{0, 0, 0, 9}
	for i, want := range wantGauge {
		var pb dto.Metric
		if err := metrics[i].Write(&pb); err != nil {
			t.Fatalf("write metric %d: %v", i, err)
		}
		if got := pb.GetGauge().GetValue(); got != want {
			t.Errorf("gauge[%d] = %v, want %v", i, got, want)
		}
	}

	wantCounter := []float64{0, 0, 0, 0}
	for i, want := range wantCounter {
		var pb dto.Metric
		if err := metrics[4+i].Write(&pb); err != nil {
			t.Fatalf("write metric %d: %v", 4+i, err)
		}
		if got := pb.GetCounter().GetValue(); got != want {
			t.Errorf("counter[%d] = %v, want %v", i, got, want)
		}
	}
}
