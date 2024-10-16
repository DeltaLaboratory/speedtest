package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	numConcurrent          = 8
	initialSize      int64 = 5 * 1024 * 1024   // 5MiB
	maxSize          int64 = 125 * 1024 * 1024 // 125MiB
	updateInterval         = 75 * time.Millisecond
	operationTimeout       = 30 * time.Second
)

// TransferMetric represents metrics for both download and upload operations
type TransferMetric struct {
	Progress float64
	Speed    float64
	Done     bool
	Err      error
}

// BandwidthOperation interface for both download and upload operations
type BandwidthOperation interface {
	OperationType() string
	Provider() Provider

	Start()
	GetMetrics() []TransferMetric
	IsDone() bool

	SetSegmentSize(size int64)
	GetSegmentSize() int64

	Reset()
}

type Provider interface {
	Name() string

	DownloadURL(size int64) string
	DownloadLimit() int64
	UploadURL(size ...int64) string
	UploadLimit() int64
}

type tickMsg time.Time

type SizeTestResult struct {
	SegmentSize  int64
	TotalSpeed   float64
	AverageSpeed float64
}

type model struct {
	bandwidth    BandwidthOperation
	quitting     bool
	progresses   []progress.Model
	segmentSize  int64
	averageSpeed float64
	testComplete bool
	totalSpeed   float64
	results      []SizeTestResult
}

func initialModel(bandwidth BandwidthOperation) model {
	bandwidth.SetSegmentSize(initialSize)
	bandwidth.Start()
	progresses := make([]progress.Model, numConcurrent)
	for i := range progresses {
		progresses[i] = progress.New(progress.WithDefaultGradient())
	}
	return model{
		bandwidth:    bandwidth,
		progresses:   progresses,
		segmentSize:  initialSize,
		averageSpeed: 0,
		totalSpeed:   0,
		testComplete: false,
		results:      []SizeTestResult{},
	}
}

func (m model) Init() tea.Cmd {
	return tick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
			return m, tea.Quit
		}
	case tickMsg:
		if m.bandwidth.IsDone() {
			if !m.testComplete {
				m.testComplete = true
				// Save result for current segment size
				m.results = append(m.results, SizeTestResult{
					SegmentSize:  m.segmentSize,
					TotalSpeed:   m.totalSpeed,
					AverageSpeed: m.averageSpeed,
				})
				nextSize := calculateNextSize(m.segmentSize, m.totalSpeed)
				if nextSize > m.segmentSize && nextSize <= maxSize {
					m.segmentSize = nextSize
					m.bandwidth.SetSegmentSize(m.segmentSize)
					m.bandwidth.Reset()
					m.bandwidth.Start()
					m.testComplete = false
					for i := range m.progresses {
						m.progresses[i].SetPercent(0)
					}
				} else {
					m.quitting = true
					return m, tea.Quit
				}
			}
		}

		metrics := m.bandwidth.GetMetrics()
		var totalSpeed float64
		for _, metric := range metrics {
			totalSpeed += metric.Speed
		}
		m.totalSpeed = totalSpeed
		m.averageSpeed = totalSpeed / float64(len(metrics))

		for i, metric := range metrics {
			m.progresses[i].SetPercent(metric.Progress)
		}

		return m, tick()
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return m.finalView()
	}

	metrics := m.bandwidth.GetMetrics()
	operationType := m.bandwidth.OperationType()

	s := fmt.Sprintf("Concurrent %s Speed Test | %s\n\n", operationType, m.bandwidth.Provider().Name())
	s += fmt.Sprintf("Current segment size: %s\n\n", formatSize(m.segmentSize))

	for i, metric := range metrics {
		if metric.Err != nil {
			s += fmt.Sprintf("%s %d: Error - %v\n\n", operationType, i+1, metric.Err)
		} else {
			s += fmt.Sprintf("%s %d: %.1f%% | Speed: %s\t%s\n",
				operationType, i+1, metric.Progress*100,
				formatSpeed(metric.Speed),
				m.progresses[i].ViewAs(metric.Progress))
		}
	}

	s += fmt.Sprintf("\nTotal Connection Speed: %s\n", formatSpeed(m.totalSpeed))
	s += fmt.Sprintf("Single Connection Speed: %s\n", formatSpeed(m.averageSpeed))
	s += "\nPress Ctrl+C to quit\n"
	return s
}

func (m model) finalView() string {
	operationType := m.bandwidth.OperationType()
	s := fmt.Sprintf("%s Test Results:\n\n", operationType)

	for _, result := range m.results {
		s += fmt.Sprintf("Segment Size: %s\n", formatSize(result.SegmentSize))
		s += fmt.Sprintf("Total Connection Speed: %s\n", formatSpeed(result.TotalSpeed))
		s += fmt.Sprintf("Single Connection Speed: %s\n", formatSpeed(result.AverageSpeed))
		s += strings.Repeat("-", 40) + "\n"
	}

	return s
}

func tick() tea.Cmd {
	return tea.Tick(updateInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type providerItem struct {
	name        string
	description string
}

func (i providerItem) Title() string       { return i.name }
func (i providerItem) Description() string { return i.description }
func (i providerItem) FilterValue() string { return i.name }

type providerSelectionModel struct {
	list     list.Model
	choice   Provider
	quitting bool
}

func initialProviderSelectionModel() providerSelectionModel {
	items := []list.Item{
		providerItem{name: "Cloudflare", description: "Cloudflare Speed test (https://speed.cloudflare.com)"},
		providerItem{name: "Benchbee", description: "Benchbee Speed test (http://speed.benchbee.co.kr)"},
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Select a provider"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = lipgloss.NewStyle().MarginLeft(2).MarginTop(1).Bold(true)

	return providerSelectionModel{list: l}
}

func (m providerSelectionModel) Init() tea.Cmd {
	return nil
}

func (m providerSelectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(msg.Height - 4) // Subtract 4 to leave room for the title and margins
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			i, ok := m.list.SelectedItem().(providerItem)
			if ok {
				switch i.name {
				case "Cloudflare":
					m.choice = Cloudflare{}
				case "Benchbee":
					m.choice = Benchbee{}
				}
			}
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m providerSelectionModel) View() string {
	return "\n" + m.list.View()
}

func main() {
	providerSelectionP := tea.NewProgram(initialProviderSelectionModel())
	providerModel, err := providerSelectionP.Run()
	if err != nil {
		fmt.Printf("Error: %v", err)
		return
	}

	selectedProvider := providerModel.(providerSelectionModel).choice
	if selectedProvider == nil {
		fmt.Println("No provider selected. Exiting.")
		return
	}

	// Run download benchmark
	fmt.Println("Running Download Benchmark...")
	p := tea.NewProgram(initialModel(NewDownloader(selectedProvider)))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
	}

	// Run upload benchmark
	fmt.Println("\nRunning Upload Benchmark...")
	p = tea.NewProgram(initialModel(NewUploader(selectedProvider)))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
	}

	// Wait for user input before exiting
	fmt.Println("\nPress Enter to exit")
	_, _ = fmt.Scanln()
}
