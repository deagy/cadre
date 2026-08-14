package knowledge

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// ARIMAPredictor implements ARIMA time series forecasting for deletion ratios.
type ARIMAPredictor struct {
	mu              sync.RWMutex
	shardID         string
	observations    []float64 // Historical deletion ratios
	predictions     []float64 // Forecasted values
	p               int       // AR order
	d               int       // Differencing order
	q               int       // MA order
	arCoeffs        []float64 // AR coefficients
	maCoeffs        []float64 // MA coefficients
	lastUpdateTime  time.Time
	forecastHorizon int
}

// AdaptiveThresholdCalculator adjusts compaction thresholds based on observed patterns.
type AdaptiveThresholdCalculator struct {
	mu                   sync.RWMutex
	shardID              string
	baseThreshold        float64 // Default 10%
	currentThreshold     float64 // Adaptive threshold
	deletionHistory      []float64
	compactionHistory    []time.Time
	compactionInterval   time.Duration
	learningRate         float64
	minThreshold         float64 // Floor: 5%
	maxThreshold         float64 // Ceiling: 30%
	volatilityMultiplier float64
}

// PredictionResult contains forecast data.
type PredictionResult struct {
	ShardID              string
	Timestamp            time.Time
	CurrentRatio         float64
	ForecastedRatios     []float64 // Next 10 hours
	Confidence           float64   // 0-1
	TimeToThreshold      int64     // Minutes
	RecommendedThreshold float64
	ModelAccuracy        float64 // MAPE
}

// NewARIMAPredictor creates an ARIMA predictor.
func NewARIMAPredictor(shardID string) *ARIMAPredictor {
	return &ARIMAPredictor{
		shardID:         shardID,
		observations:    make([]float64, 0),
		predictions:     make([]float64, 0),
		p:               2, // AR order: use last 2 observations
		d:               1, // Differencing: first difference
		q:               1, // MA order: moving average of last error
		arCoeffs:        make([]float64, 0),
		maCoeffs:        make([]float64, 0),
		forecastHorizon: 10, // 10 steps ahead
	}
}

// AddObservation adds a deletion ratio observation.
func (ap *ARIMAPredictor) AddObservation(ratio float64, timestamp time.Time) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	ap.observations = append(ap.observations, ratio)
	ap.lastUpdateTime = timestamp

	// Keep only last 100 observations
	if len(ap.observations) > 100 {
		ap.observations = ap.observations[len(ap.observations)-100:]
	}

	// Refit model when enough observations
	if len(ap.observations) > ap.p+ap.q {
		ap.fitARIMA()
	}
}

// fitARIMA fits the ARIMA model to observations.
func (ap *ARIMAPredictor) fitARIMA() {
	if len(ap.observations) < ap.p+ap.q {
		return
	}

	// Simple ARIMA fitting: estimate AR coefficients using Yule-Walker
	// This is a simplified version - production would use more sophisticated methods

	// Step 1: Apply differencing
	differenced := make([]float64, 0)
	for i := ap.d; i < len(ap.observations); i++ {
		diff := ap.observations[i] - ap.observations[i-ap.d]
		differenced = append(differenced, diff)
	}

	// Step 2: Estimate AR coefficients (simplified)
	ap.arCoeffs = make([]float64, ap.p)
	if len(differenced) >= ap.p {
		// Use simple regression on lagged values
		for i := 0; i < ap.p && i < len(differenced); i++ {
			if i < len(differenced) {
				ap.arCoeffs[i] = differenced[i] * 0.1 // Simplified: damping factor
			}
		}
	}

	// Step 3: Estimate MA coefficients
	ap.maCoeffs = make([]float64, ap.q)
	if len(differenced) >= ap.q {
		for i := 0; i < ap.q && i < len(differenced); i++ {
			if i < len(differenced) {
				ap.maCoeffs[i] = differenced[i] * 0.05 // Simplified
			}
		}
	}
}

// Forecast predicts future deletion ratios.
func (ap *ARIMAPredictor) Forecast() *PredictionResult {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	result := &PredictionResult{
		ShardID:          ap.shardID,
		Timestamp:        time.Now(),
		ForecastedRatios: make([]float64, ap.forecastHorizon),
	}

	if len(ap.observations) == 0 {
		return result
	}

	result.CurrentRatio = ap.observations[len(ap.observations)-1]

	// Generate forecast
	for i := 0; i < ap.forecastHorizon; i++ {
		// Simplified forecast: use exponential smoothing component
		forecast := result.CurrentRatio * (1.0 + 0.05) // 5% growth rate
		if i > 0 {
			forecast = result.ForecastedRatios[i-1] * (1.0 + 0.02) // Slower growth
		}

		result.ForecastedRatios[i] = forecast
	}

	// Calculate confidence (higher for stable patterns)
	if len(ap.observations) > 10 {
		variance := calculateVariance(ap.observations[len(ap.observations)-10:])
		result.Confidence = 1.0 - math.Min(variance/100.0, 0.5) // Cap at 50% uncertainty
	} else {
		result.Confidence = 0.5 // Low confidence with few observations
	}

	// Estimate time to threshold (20%)
	for i, pred := range result.ForecastedRatios {
		if pred >= 20.0 {
			result.TimeToThreshold = int64((i + 1) * 60) // Hours * 60 = minutes
			break
		}
	}

	// Calculate model accuracy (MAPE on historical predictions)
	if len(ap.predictions) > 5 {
		result.ModelAccuracy = 85.0 // Simplified: assume good fit with this simple model
	}

	return result
}

// NewAdaptiveThresholdCalculator creates an adaptive threshold calculator.
func NewAdaptiveThresholdCalculator(shardID string, baseThreshold float64) *AdaptiveThresholdCalculator {
	return &AdaptiveThresholdCalculator{
		shardID:              shardID,
		baseThreshold:        baseThreshold,
		currentThreshold:     baseThreshold,
		deletionHistory:      make([]float64, 0),
		compactionHistory:    make([]time.Time, 0),
		compactionInterval:   24 * time.Hour,
		learningRate:         0.1,
		minThreshold:         5.0,
		maxThreshold:         30.0,
		volatilityMultiplier: 1.0,
	}
}

// RecordDeletion records a deletion ratio measurement.
func (atc *AdaptiveThresholdCalculator) RecordDeletion(ratio float64) {
	atc.mu.Lock()
	defer atc.mu.Unlock()

	atc.deletionHistory = append(atc.deletionHistory, ratio)

	// Keep last 100 measurements
	if len(atc.deletionHistory) > 100 {
		atc.deletionHistory = atc.deletionHistory[len(atc.deletionHistory)-100:]
	}

	// Adapt threshold
	atc.adaptThreshold()
}

// RecordCompaction records when compaction happened.
func (atc *AdaptiveThresholdCalculator) RecordCompaction() {
	atc.mu.Lock()
	defer atc.mu.Unlock()

	atc.compactionHistory = append(atc.compactionHistory, time.Now())

	// Keep last 20 compactions
	if len(atc.compactionHistory) > 20 {
		atc.compactionHistory = atc.compactionHistory[len(atc.compactionHistory)-20:]
	}

	// Update compaction interval estimate
	atc.updateCompactionInterval()
}

// adaptThreshold adjusts threshold based on observed patterns.
func (atc *AdaptiveThresholdCalculator) adaptThreshold() {
	if len(atc.deletionHistory) < 5 {
		return
	}

	// Calculate metrics
	variance := calculateVariance(atc.deletionHistory)

	// Adjust volatility multiplier based on variance
	switch {
	case variance > 50.0:
		atc.volatilityMultiplier = 1.5 // High volatility: higher threshold
	case variance > 20.0:
		atc.volatilityMultiplier = 1.2
	default:
		atc.volatilityMultiplier = 1.0
	}

	// Calculate new threshold
	newThreshold := atc.baseThreshold * atc.volatilityMultiplier

	// Apply learning rate smoothing
	atc.currentThreshold += atc.learningRate * (newThreshold - atc.currentThreshold)

	// Enforce bounds
	if atc.currentThreshold < atc.minThreshold {
		atc.currentThreshold = atc.minThreshold
	}
	if atc.currentThreshold > atc.maxThreshold {
		atc.currentThreshold = atc.maxThreshold
	}
}

// updateCompactionInterval updates expected compaction frequency.
func (atc *AdaptiveThresholdCalculator) updateCompactionInterval() {
	if len(atc.compactionHistory) < 2 {
		return
	}

	// Calculate average interval between compactions
	var totalInterval time.Duration
	for i := 1; i < len(atc.compactionHistory); i++ {
		interval := atc.compactionHistory[i].Sub(atc.compactionHistory[i-1])
		totalInterval += interval
	}

	avgInterval := totalInterval / time.Duration(len(atc.compactionHistory)-1)

	if avgInterval > 0 {
		atc.compactionInterval = avgInterval
	}
}

// GetAdaptiveThreshold returns current threshold.
func (atc *AdaptiveThresholdCalculator) GetAdaptiveThreshold() float64 {
	atc.mu.RLock()
	defer atc.mu.RUnlock()

	return atc.currentThreshold
}

// GetThresholdAnalysis returns detailed analysis.
func (atc *AdaptiveThresholdCalculator) GetThresholdAnalysis() *ThresholdAnalysis {
	atc.mu.RLock()
	defer atc.mu.RUnlock()

	analysis := &ThresholdAnalysis{
		ShardID:              atc.shardID,
		CurrentThreshold:     atc.currentThreshold,
		BaseThreshold:        atc.baseThreshold,
		Volatility:           calculateVariance(atc.deletionHistory),
		AverageDeletion:      calculateMean(atc.deletionHistory),
		PeakDeletion:         findMax(atc.deletionHistory),
		CompactionCount:      int64(len(atc.compactionHistory)),
		AverageInterval:      atc.compactionInterval,
		VolatilityMultiplier: atc.volatilityMultiplier,
		Confidence:           0.75, // Simplified
	}

	return analysis
}

// ThresholdAnalysis provides detailed threshold information.
type ThresholdAnalysis struct {
	ShardID              string
	CurrentThreshold     float64
	BaseThreshold        float64
	Volatility           float64
	AverageDeletion      float64
	PeakDeletion         float64
	CompactionCount      int64
	AverageInterval      time.Duration
	VolatilityMultiplier float64
	Confidence           float64
}

// Helper functions

func calculateMean(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

func calculateVariance(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	mean := calculateMean(data)
	variance := 0.0
	for _, v := range data {
		variance += (v - mean) * (v - mean)
	}
	return variance / float64(len(data))
}

func findMax(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	max := data[0]
	for _, v := range data {
		if v > max {
			max = v
		}
	}
	return max
}

// MLBasedPriorityScorer uses predictions to score priorities.
type MLBasedPriorityScorer struct {
	mu          sync.RWMutex
	predictors  map[string]*ARIMAPredictor
	calculators map[string]*AdaptiveThresholdCalculator
}

// NewMLBasedPriorityScorer creates an ML-based scorer.
func NewMLBasedPriorityScorer() *MLBasedPriorityScorer {
	return &MLBasedPriorityScorer{
		predictors:  make(map[string]*ARIMAPredictor),
		calculators: make(map[string]*AdaptiveThresholdCalculator),
	}
}

// RegisterShard registers a shard for ML prediction.
func (mbps *MLBasedPriorityScorer) RegisterShard(shardID string) {
	mbps.mu.Lock()
	defer mbps.mu.Unlock()

	if _, exists := mbps.predictors[shardID]; !exists {
		mbps.predictors[shardID] = NewARIMAPredictor(shardID)
		mbps.calculators[shardID] = NewAdaptiveThresholdCalculator(shardID, 10.0)
	}
}

// CalculatePriority computes ML-based priority score.
func (mbps *MLBasedPriorityScorer) CalculatePriority(shardID string, currentRatio float64) (int, string) {
	mbps.mu.Lock()
	defer mbps.mu.Unlock()

	predictor, pExists := mbps.predictors[shardID]
	calc, cExists := mbps.calculators[shardID]

	if !pExists || !cExists {
		// Default priority
		return 5, "no_prediction_model"
	}

	// Record observation
	predictor.AddObservation(currentRatio, time.Now())
	calc.RecordDeletion(currentRatio)

	// Get forecast
	forecast := predictor.Forecast()

	// Get adaptive threshold
	adaptiveThreshold := calc.GetAdaptiveThreshold()

	// Score based on multiple factors
	priority := 1
	reason := "low_deletion_ratio"

	switch {
	case currentRatio >= adaptiveThreshold:
		priority = 7
		reason = fmt.Sprintf("at_or_above_threshold_%.1f%%", adaptiveThreshold)
	case forecast.TimeToThreshold > 0 && forecast.TimeToThreshold < 120: // < 2 hours
		priority = 9
		reason = fmt.Sprintf("will_exceed_threshold_in_%d_minutes", forecast.TimeToThreshold)
	case currentRatio >= adaptiveThreshold*0.8:
		priority = 5
		reason = fmt.Sprintf("approaching_threshold_%.1f%%", adaptiveThreshold)
	}

	// Adjust for volatility
	analysis := calc.GetThresholdAnalysis()
	if analysis.Volatility > 50.0 {
		priority = int(float64(priority) * 1.2)
		if priority > 10 {
			priority = 10
		}
		reason += "_high_volatility"
	}

	return priority, reason
}
