package metrics

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Labels map[string]string

type GaugeSample struct {
	Name   string
	Labels Labels
	Value  float64
}

type GaugeCollector func(ctx context.Context) ([]GaugeSample, error)

type Registry struct {
	mu             sync.RWMutex
	counters       map[string]float64
	histograms     map[string]*histogramValue
	gauges         map[string]float64
	gaugeCollector GaugeCollector
	buckets        []float64
}

type histogramValue struct {
	counts []uint64
	sum    float64
	count  uint64
}

func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]float64),
		histograms: make(map[string]*histogramValue),
		gauges:     make(map[string]float64),
		buckets:    []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}
}

func (r *Registry) SetGaugeCollector(collector GaugeCollector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gaugeCollector = collector
}

func (r *Registry) Inc(name string, labels Labels) {
	r.Add(name, labels, 1)
}

func (r *Registry) Add(name string, labels Labels, value float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[seriesKey(name, labels)] += value
}

func (r *Registry) SetGauge(name string, labels Labels, value float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[seriesKey(name, labels)] = value
}

func (r *Registry) Observe(name string, labels Labels, value float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := seriesKey(name, labels)
	histogram := r.histograms[key]
	if histogram == nil {
		histogram = &histogramValue{counts: make([]uint64, len(r.buckets)+1)}
		r.histograms[key] = histogram
	}
	for i, bucket := range r.buckets {
		if value <= bucket {
			histogram.counts[i]++
		}
	}
	histogram.counts[len(r.buckets)]++
	histogram.count++
	histogram.sum += value
}

func (r *Registry) ObserveDuration(name string, labels Labels, start time.Time) {
	r.Observe(name, labels, time.Since(start).Seconds())
}

func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := r.collectGauges(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write(r.render())
	})
}

func (r *Registry) collectGauges(ctx context.Context) error {
	r.mu.RLock()
	collector := r.gaugeCollector
	r.mu.RUnlock()
	if collector == nil {
		return nil
	}
	samples, err := collector(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sample := range samples {
		r.gauges[seriesKey(sample.Name, sample.Labels)] = sample.Value
	}
	return nil
}

func (r *Registry) render() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var buf bytes.Buffer
	writeCounters(&buf, r.counters)
	writeGauges(&buf, r.gauges)
	writeHistograms(&buf, r.histograms, r.buckets)
	return buf.Bytes()
}

func writeCounters(buf *bytes.Buffer, counters map[string]float64) {
	keys := sortedKeys(counters)
	seen := make(map[string]struct{})
	for _, key := range keys {
		name, labels := splitSeriesKey(key)
		if _, ok := seen[name]; !ok {
			fmt.Fprintf(buf, "# TYPE %s counter\n", name)
			seen[name] = struct{}{}
		}
		fmt.Fprintf(buf, "%s%s %s\n", name, formatLabels(labels), formatFloat(counters[key]))
	}
}

func writeGauges(buf *bytes.Buffer, gauges map[string]float64) {
	keys := sortedKeys(gauges)
	seen := make(map[string]struct{})
	for _, key := range keys {
		name, labels := splitSeriesKey(key)
		if _, ok := seen[name]; !ok {
			fmt.Fprintf(buf, "# TYPE %s gauge\n", name)
			seen[name] = struct{}{}
		}
		fmt.Fprintf(buf, "%s%s %s\n", name, formatLabels(labels), formatFloat(gauges[key]))
	}
}

func writeHistograms(buf *bytes.Buffer, histograms map[string]*histogramValue, buckets []float64) {
	keys := sortedKeys(histograms)
	seen := make(map[string]struct{})
	for _, key := range keys {
		name, labels := splitSeriesKey(key)
		if _, ok := seen[name]; !ok {
			fmt.Fprintf(buf, "# TYPE %s histogram\n", name)
			seen[name] = struct{}{}
		}
		histogram := histograms[key]
		for i, bucket := range buckets {
			bucketLabels := cloneLabels(labels)
			bucketLabels["le"] = formatFloat(bucket)
			fmt.Fprintf(buf, "%s_bucket%s %d\n", name, formatLabels(bucketLabels), histogram.counts[i])
		}
		infLabels := cloneLabels(labels)
		infLabels["le"] = "+Inf"
		fmt.Fprintf(buf, "%s_bucket%s %d\n", name, formatLabels(infLabels), histogram.counts[len(buckets)])
		fmt.Fprintf(buf, "%s_sum%s %s\n", name, formatLabels(labels), formatFloat(histogram.sum))
		fmt.Fprintf(buf, "%s_count%s %d\n", name, formatLabels(labels), histogram.count)
	}
}

func seriesKey(name string, labels Labels) string {
	return name + "|" + encodeLabels(labels)
}

func splitSeriesKey(key string) (string, Labels) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return parts[0], nil
	}
	labels := make(Labels)
	for _, pair := range strings.Split(parts[1], ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			labels[kv[0]] = kv[1]
		}
	}
	return parts[0], labels
}

func encodeLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

func formatLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapeLabelValue(labels[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneLabels(labels Labels) Labels {
	clone := make(Labels, len(labels)+1)
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
