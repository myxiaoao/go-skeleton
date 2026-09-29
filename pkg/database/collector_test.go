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
// 按指标名断言类型与取值：max_conns 应反映配置值，其余指标在没有真实 acquire
// 时为 0——足以验证 Stat() 字段到指标的映射没接错线，不依赖 Collect 的输出顺序。
func TestCollectorCollectReadsPoolStat(t *testing.T) {
	m, err := Init(t.Context(), Config{DSN: unreachableDSN, MaxConns: 9})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(m.Collector())
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	got := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		got[f.GetName()] = f
	}

	want := []struct {
		name  string
		typ   dto.MetricType
		value float64
	}{
		{"go_skeleton_db_pool_acquired_conns", dto.MetricType_GAUGE, 0},
		{"go_skeleton_db_pool_idle_conns", dto.MetricType_GAUGE, 0},
		{"go_skeleton_db_pool_total_conns", dto.MetricType_GAUGE, 0},
		{"go_skeleton_db_pool_max_conns", dto.MetricType_GAUGE, 9},
		{"go_skeleton_db_pool_acquire_count_total", dto.MetricType_COUNTER, 0},
		{"go_skeleton_db_pool_empty_acquire_count_total", dto.MetricType_COUNTER, 0},
		{"go_skeleton_db_pool_canceled_acquire_count_total", dto.MetricType_COUNTER, 0},
		{"go_skeleton_db_pool_acquire_duration_seconds_total", dto.MetricType_COUNTER, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("gathered %d metric families, want %d", len(got), len(want))
	}
	for _, w := range want {
		f, ok := got[w.name]
		if !ok {
			t.Errorf("missing metric %s", w.name)
			continue
		}
		if f.GetType() != w.typ {
			t.Errorf("%s type = %v, want %v", w.name, f.GetType(), w.typ)
		}
		metric := f.GetMetric()[0]
		v := metric.GetGauge().GetValue()
		if w.typ == dto.MetricType_COUNTER {
			v = metric.GetCounter().GetValue()
		}
		if v != w.value {
			t.Errorf("%s = %v, want %v", w.name, v, w.value)
		}
	}
}
