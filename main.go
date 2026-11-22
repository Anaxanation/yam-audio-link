package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	rate     = 48000
	frames   = 288
	pcmBytes = frames * 4
	magic    = "YAL1"
)

type settings struct {
	Mode string `json:"mode"`
	Peer string `json:"peer"`
	Port int    `json:"port"`
	PCM  string `json:"pcm"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := readSettings()
	if err == nil {
		err = run(ctx, config)
	}
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readSettings() (settings, error) {
	config := settings{Mode: "recv", Port: 47668}
	if args := os.Args[1:]; len(args) > 0 {
		if len(args) < 2 || len(args) > 4 {
			return config, errors.New("usage: audio-link recv|send|test PEER_IPV4 [PORT] [PCM]")
		}
		config.Mode, config.Peer = args[0], args[1]
		if len(args) > 2 {
			port, err := strconv.Atoi(args[2])
			if err != nil {
				return config, err
			}
			config.Port = port
		}
		if len(args) > 3 {
			config.PCM = args[3]
		}
		return config, nil
	}
	file, err := os.Open(os.Getenv("ALICE_CONFIG_PATH"))
	if err != nil {
		return config, fmt.Errorf("open Homebrew settings: %w", err)
	}
	defer file.Close()
	err = json.NewDecoder(io.LimitReader(file, 8192)).Decode(&config)
	return config, err
}

func run(ctx context.Context, cfg settings) error {
	ip := net.ParseIP(strings.TrimSpace(cfg.Peer)).To4()
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) || cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("set the peer speaker IPv4 address and a UDP port from 1 to 65535")
	}
	if cfg.Mode == "recv" {
		if cfg.PCM == "" {
			cfg.PCM = "default"
		}
		return receive(ctx, ip, cfg)
	}
	if cfg.Mode != "test" && cfg.Mode != "send" {
		return fmt.Errorf("unknown mode %s", cfg.Mode)
	}
	if cfg.Mode == "send" {
		if cfg.PCM == "" {
			cfg.PCM = "plughw:CARD=Loopback,DEV=1"
		}
		if !strings.HasPrefix(cfg.PCM, "plughw:CARD=Loopback,DEV=") && !strings.HasPrefix(cfg.PCM, "hw:CARD=Loopback,DEV=") {
			return errors.New("send requires an ALSA Loopback PCM carrying speaker output")
		}
		cards, _ := os.ReadFile("/proc/asound/cards")
		if !strings.Contains(string(cards), "[Loopback ") {
			return errors.New("ALSA Loopback card absent; speaker output capture unavailable")
		}
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: cfg.Port})
	if err != nil {
		return err
	}
	defer conn.Close()
	packet := make([]byte, len(magic)+pcmBytes)
	copy(packet, magic)
	if cfg.Mode == "test" {
		ticker := time.NewTicker(time.Second * frames / rate)
		defer ticker.Stop()
		for offset := 0; offset < rate*3; offset += frames {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
			for frame := 0; frame < frames; frame++ {
				n := offset + frame
				var sample int16
				if n%rate < rate/4 {
					sample = int16(2200 * math.Sin(2*math.Pi*660*float64(n)/rate))
				}
				start := len(magic) + frame*4
				binary.LittleEndian.PutUint16(packet[start:], uint16(sample))
				binary.LittleEndian.PutUint16(packet[start+2:], uint16(sample))
			}
			if _, err = conn.Write(packet); err != nil {
				return err
			}
		}
		return nil
	}
	record := alsa(ctx, "arecord", cfg.PCM)
	audio, err := record.StdoutPipe()
	if err != nil {
		return err
	}
	if err = record.Start(); err != nil {
		return err
	}
	defer func() { _ = record.Process.Kill(); _ = record.Wait() }()
	for {
		if _, err = io.ReadFull(audio, packet[len(magic):]); err != nil {
			return fmt.Errorf("capture stopped: %w", err)
		}
		if _, err = conn.Write(packet); err != nil {
			return err
		}
	}
}

func alsa(ctx context.Context, tool, pcm string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, tool, "-q", "-D", pcm, "-t", "raw", "-f", "S16_LE", "-c", "2", "-r", "48000", "--buffer-time=120000", "--period-time=24000")
	cmd.Stderr = os.Stderr
	return cmd
}

func receive(ctx context.Context, peer net.IP, cfg settings) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: cfg.Port})
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(48 * 1024)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var player *exec.Cmd
	var audio io.WriteCloser
	closePlayer := func() {
		if player != nil {
			_ = audio.Close()
			_ = player.Wait()
			player = nil
		}
	}
	defer closePlayer()
	packet := make([]byte, len(magic)+pcmBytes+1)
	fmt.Printf("Listening for %s on UDP %d\n", peer, cfg.Port)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		size, sender, err := conn.ReadFromUDP(packet)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				closePlayer()
				continue
			}
			return err
		}
		if !sender.IP.Equal(peer) || size != len(packet)-1 || string(packet[:len(magic)]) != magic {
			continue
		}
		if player == nil {
			cmd := alsa(ctx, "aplay", cfg.PCM)
			audio, err = cmd.StdinPipe()
			if err != nil {
				return err
			}
			if err = cmd.Start(); err != nil {
				_ = audio.Close()
				return err
			}
			player = cmd
		}
		if _, err = audio.Write(packet[len(magic):size]); err != nil {
			return fmt.Errorf("playback stopped: %w", err)
		}
	}
}
