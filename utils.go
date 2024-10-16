package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type SpeedMeter struct {
	mutex      sync.Mutex
	windowSize time.Duration
	lastUpdate time.Time
	total      int64
	current    float64
}

func NewSpeedMeter(windowSize time.Duration) *SpeedMeter {
	return &SpeedMeter{
		windowSize: windowSize,
		lastUpdate: time.Now(),
	}
}

func (m *SpeedMeter) Add(n int64) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(m.lastUpdate)

	if elapsed >= m.windowSize {
		m.current = float64(n) / m.windowSize.Seconds()
		m.total = n
	} else {
		decayFactor := float64(m.windowSize-elapsed) / float64(m.windowSize)
		m.total = int64(float64(m.total)*decayFactor) + n
		m.current = float64(m.total) / m.windowSize.Seconds()
	}

	m.lastUpdate = now
}

func (m *SpeedMeter) Value() float64 {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(m.lastUpdate)

	if elapsed >= m.windowSize {
		return 0
	}

	decayFactor := float64(m.windowSize-elapsed) / float64(m.windowSize)
	return m.current * decayFactor
}

func formatSpeed(speedMbps float64) string {
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	speed := speedMbps * 1e6 // Convert Mbps to bps
	index := 0

	for speed >= 1000 && index < len(units)-1 {
		speed /= 1000
		index++
	}

	return fmt.Sprintf("%.2f %s", speed, units[index])
}

func formatSize(sizeBytes int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(sizeBytes)
	index := 0

	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}

	return fmt.Sprintf("%.2f %s", size, units[index])
}

func calculateNextSize(currentSize int64, speed float64) int64 {
	if speed > 20 {
		nextSize := int64(math.Min(float64(currentSize)*5, float64(maxSize)))
		return nextSize
	}
	return currentSize
}
