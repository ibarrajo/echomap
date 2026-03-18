package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	echomapv1 "github.com/elninja/echomap/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	serverAddr := "localhost:50051"
	if len(os.Args) > 1 {
		serverAddr = os.Args[1]
	}

	clientID := "echomap-cli"
	if len(os.Args) > 2 {
		clientID = os.Args[2]
	}

	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	client := echomapv1.NewEchoMapClient(conn)
	totalStart := time.Now()

	// Step 1: Fetch challenge
	challengeStart := time.Now()
	challenge, err := client.FetchChallenge(context.Background(), &echomapv1.ChallengeRequest{
		ClientId: clientID,
	})
	if err != nil {
		log.Fatalf("FetchChallenge: %v", err)
	}
	challengeDur := time.Since(challengeStart)

	// Step 2: Ping all probe targets in parallel
	fmt.Printf("Pinging %d probes in parallel...\n", len(challenge.Targets))
	type probeResult struct {
		target *echomapv1.ProbeTarget
		rtts   []int32
	}
	pingStart := time.Now()
	results := make([]probeResult, len(challenge.Targets))
	var wg sync.WaitGroup
	for i, target := range challenge.Targets {
		wg.Add(1)
		go func(idx int, t *echomapv1.ProbeTarget) {
			defer wg.Done()
			results[idx] = probeResult{target: t, rtts: pingProbe(t.Host, int(t.Port), int(t.PingCount))}
		}(i, target)
	}
	wg.Wait()
	pingDur := time.Since(pingStart)

	var measurements []*echomapv1.ProbeMeasurement
	for _, r := range results {
		measurements = append(measurements, &echomapv1.ProbeMeasurement{
			ProbeId: r.target.Id,
			RttsUs:  r.rtts,
		})
		medianRTT := median(r.rtts)
		fmt.Printf("  %-8s %7.1fms  [%s]\n",
			r.target.Id, float64(medianRTT)/1000.0, formatRTTs(r.rtts))
	}

	// Step 3: Submit measurements
	submitStart := time.Now()
	resp, err := client.SubmitMeasurement(context.Background(), &echomapv1.MeasurementRequest{
		ChallengeId:  challenge.ChallengeId,
		Token:        challenge.Token,
		Measurements: measurements,
	})
	if err != nil {
		log.Fatalf("SubmitMeasurement: %v", err)
	}
	submitDur := time.Since(submitStart)
	totalDur := time.Since(totalStart)

	// Step 4: Display results
	fmt.Println("\n=== GEOLOCATION RESULT ===")
	fmt.Printf("  Verdict:    %s\n", resp.Verdict.Status.String())
	fmt.Printf("  Location:   %s\n", resp.Region.Label)
	fmt.Printf("  Lat/Lon:    %.4f, %.4f\n", resp.Region.Lat, resp.Region.Lon)
	fmt.Printf("  Accuracy:   ±%.0f km radius\n", resp.Region.RadiusKm)

	fmt.Println("\n  Confidence:")
	fmt.Printf("    Physics (speed of light):   %5.1f%%\n", resp.Verdict.PhysicsConfidence*100)
	fmt.Printf("    Physics + dataset:          %5.1f%%\n", resp.Verdict.Confidence*100)

	fmt.Printf("    Nearest probe:              %s\n", resp.Verdict.NearestProbe)

	// Probe details
	fmt.Println("\n  Probe Details:")
	fmt.Printf("  %-8s  %-10s  %-12s  %s\n", "PROBE", "RTT", "MAX DIST", "SPEED-OF-LIGHT BOUND")
	for _, pr := range resp.ProbeResults {
		fmt.Printf("  %-8s  %7.1fms  %8.0f km   You are within %.0f km of this probe\n",
			pr.ProbeId, pr.RttMs, pr.MaxDistanceKm, pr.MaxDistanceKm)
	}

	if len(resp.Exclusions) > 0 {
		fmt.Println("\n  You are NOT in:")
		for _, e := range resp.Exclusions {
			fmt.Printf("    - %s (%.0f%% confidence)\n", e.Region, e.Confidence*100)
		}
	}

	if resp.Spoofing != nil && (resp.Spoofing.VpnLikely || resp.Spoofing.JitterAbnormal || resp.Spoofing.RatioInconsistent || resp.Spoofing.PhysicallyImpossible) {
		fmt.Println("\n  Spoofing Flags:")
		if resp.Spoofing.VpnLikely {
			fmt.Println("    ! VPN/proxy detected (all probes have similar high RTTs)")
		}
		if resp.Spoofing.JitterAbnormal {
			fmt.Println("    ! Abnormal jitter (RTTs too consistent — possible artificial delay)")
		}
		if resp.Spoofing.RatioInconsistent {
			fmt.Println("    ! RTT ratios inconsistent with any real geographic location")
		}
		if resp.Spoofing.PhysicallyImpossible {
			fmt.Println("    ! Physically impossible — RTTs violate speed of light")
		}
	}

	// Performance metrics
	totalPings := len(challenge.Targets) * int(challenge.Targets[0].PingCount)
	fmt.Println("\n  Performance:")
	fmt.Printf("    Total time:        %dms\n", totalDur.Milliseconds())
	fmt.Printf("    Challenge fetch:   %dms (1 gRPC call)\n", challengeDur.Milliseconds())
	fmt.Printf("    Probe pings:       %dms (%d probes × %d pings = %d TCP connects, parallel)\n",
		pingDur.Milliseconds(), len(challenge.Targets), challenge.Targets[0].PingCount, totalPings)
	fmt.Printf("    Submit + compute:  %dms (1 gRPC call)\n", submitDur.Milliseconds())
	fmt.Printf("    Total requests:    %d (2 gRPC + %d TCP)\n", 2+totalPings, totalPings)
}

// pingProbe measures RTT to a host:port using TCP connect timing.
func pingProbe(host string, port, count int) []int32 {
	addr := fmt.Sprintf("%s:%d", host, port)
	rtts := make([]int32, 0, count)

	for i := 0; i < count; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		elapsed := time.Since(start)

		if err != nil {
			// If probe is unreachable, record a high RTT to indicate distance/failure
			rtts = append(rtts, 999999) // ~1 second marker
			continue
		}
		conn.Close()

		rtts = append(rtts, int32(elapsed.Microseconds()))
	}

	if len(rtts) == 0 {
		rtts = append(rtts, 999999)
	}
	return rtts
}

func median(vals []int32) int32 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int32, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func formatRTTs(rtts []int32) string {
	s := ""
	for i, r := range rtts {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%.2fms", float64(r)/1000.0)
	}
	return s
}
