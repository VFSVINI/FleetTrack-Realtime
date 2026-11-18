// Comando simulator: simula dezenas de veículos circulando pelo Recife e enviando
// telemetria contínua ao ingestor, para testes de carga e demonstração do dashboard.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"fleettrack/pkg/logger"
	"fleettrack/pkg/models"
)

// Área de circulação (Grande Recife).
const (
	minLat, maxLat = -8.15, -8.00
	minLon, maxLon = -34.98, -34.85
)

type vehicle struct {
	id      string
	lat     float64
	lon     float64
	heading float64 // radianos
	speed   float64
	target  float64
	fuel    float64
	speeder bool
	parked  int // ticks restantes estacionado
	rng     *rand.Rand
}

func newVehicle(i int, seed int64) *vehicle {
	rng := rand.New(rand.NewSource(seed + int64(i)))
	return &vehicle{
		id:      fmt.Sprintf("VEH-%03d", i+1),
		lat:     minLat + rng.Float64()*(maxLat-minLat),
		lon:     minLon + rng.Float64()*(maxLon-minLon),
		heading: rng.Float64() * 2 * math.Pi,
		target:  20 + rng.Float64()*45,
		fuel:    20 + rng.Float64()*80,
		speeder: rng.Float64() < 0.2,
		rng:     rng,
	}
}

func (v *vehicle) pickTarget() float64 {
	if v.speeder && v.rng.Float64() < 0.3 {
		return 105 + v.rng.Float64()*25 // excesso de velocidade
	}
	return 20 + v.rng.Float64()*45
}

// step avança a simulação em simSeconds segundos simulados.
func (v *vehicle) step(simSeconds float64) models.Telemetry {
	engine := models.EngineOn

	switch {
	case v.parked > 0:
		v.parked--
		v.speed, engine = 0, models.EngineOff
	case v.rng.Float64() < 0.01:
		v.parked = 10 + v.rng.Intn(20)
		v.speed, engine = 0, models.EngineOff
	default:
		if v.rng.Float64() < 0.05 {
			v.target = v.pickTarget()
		}
		v.speed += (v.target - v.speed) * 0.3
		v.heading += (v.rng.Float64() - 0.5) * 0.4

		distKm := v.speed * simSeconds / 3600
		v.lat += distKm * math.Cos(v.heading) / 111.32
		v.lon += distKm * math.Sin(v.heading) / (111.32 * math.Cos(v.lat*math.Pi/180))
		if v.lat < minLat || v.lat > maxLat || v.lon < minLon || v.lon > maxLon {
			v.heading += math.Pi // reflete ao atingir a borda
			v.lat = math.Min(math.Max(v.lat, minLat), maxLat)
			v.lon = math.Min(math.Max(v.lon, minLon), maxLon)
		}
		v.fuel -= distKm * 0.4 // ~0,4% por km
		if v.speed < 2 {
			engine = models.EngineIdle
		}
	}
	if v.fuel < 3 {
		v.fuel = 100 // reabastece
	}

	return models.Telemetry{
		VehicleID: v.id, Latitude: v.lat, Longitude: v.lon,
		SpeedKmh: math.Round(v.speed*10) / 10, FuelLevel: math.Round(v.fuel*10) / 10,
		EngineStatus: engine, Timestamp: time.Now().UTC(),
	}
}

type sender struct {
	client *http.Client
	url    string
	sent   atomic.Uint64
	ok     atomic.Uint64
	failed atomic.Uint64
}

func (s *sender) post(ctx context.Context, path string, payload any, n int) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+path, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	s.sent.Add(uint64(n))
	resp, err := s.client.Do(req)
	if err != nil {
		s.failed.Add(uint64(n))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		s.ok.Add(uint64(n))
	} else {
		s.failed.Add(uint64(n))
	}
}

func main() {
	url := flag.String("url", "http://localhost:8080", "URL base do ingestor")
	count := flag.Int("vehicles", 30, "quantidade de veículos simulados")
	interval := flag.Duration("interval", time.Second, "intervalo entre envios de cada veículo")
	timescale := flag.Float64("timescale", 30, "fator de aceleração do tempo simulado (para o movimento ser visível no mapa)")
	batch := flag.Bool("batch", false, "enviar uma requisição em lote por tick (em vez de uma por veículo)")
	duration := flag.Duration("duration", 0, "duração do teste (0 = até Ctrl+C)")
	seed := flag.Int64("seed", time.Now().UnixNano(), "semente aleatória")
	flag.Parse()

	log := logger.New("simulator")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	s := &sender{
		url: *url,
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: &http.Transport{MaxIdleConns: *count * 2, MaxIdleConnsPerHost: *count * 2, IdleConnTimeout: 30 * time.Second},
		},
	}
	vehicles := make([]*vehicle, *count)
	for i := range vehicles {
		vehicles[i] = newVehicle(i, *seed)
	}
	simSeconds := interval.Seconds() * *timescale
	log.Info("simulação iniciada", "vehicles", *count, "interval", interval.String(), "batch", *batch, "url", *url)

	var wg sync.WaitGroup
	if *batch {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(*interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					points := make([]models.Telemetry, 0, len(vehicles))
					for _, v := range vehicles {
						points = append(points, v.step(simSeconds))
					}
					for len(points) > 0 { // respeita o limite de 500 itens por lote
						n := min(len(points), 500)
						s.post(ctx, "/v1/telemetry/batch", points[:n], n)
						points = points[n:]
					}
				}
			}
		}()
	} else {
		for _, v := range vehicles {
			wg.Add(1)
			go func(v *vehicle) {
				defer wg.Done()
				// Jitter inicial evita que todos os veículos disparem no mesmo instante.
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(v.rng.Int63n(int64(*interval)))):
				}
				ticker := time.NewTicker(*interval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						s.post(ctx, "/v1/telemetry", v.step(simSeconds), 1)
					}
				}
			}(v)
		}
	}

	// Relatório periódico de throughput.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var last uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sent := s.sent.Load()
				log.Info("progresso", "sent", sent, "ok", s.ok.Load(), "failed", s.failed.Load(),
					"msgs_per_sec", float64(sent-last)/5)
				last = sent
			}
		}
	}()

	wg.Wait()
	log.Info("simulação encerrada", "sent", s.sent.Load(), "ok", s.ok.Load(), "failed", s.failed.Load())
}
