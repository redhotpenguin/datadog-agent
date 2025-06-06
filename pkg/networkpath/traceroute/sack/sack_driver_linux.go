// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

//go:build linux

package sack

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"syscall"
	"time"

	"github.com/google/gopacket/layers"
	"golang.org/x/net/ipv4"

	"github.com/DataDog/datadog-agent/pkg/networkpath/traceroute/common"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

type sackDriver struct {
	tcpConn   *ipv4.RawConn
	icmpConn  *ipv4.RawConn
	sendTimes []time.Time
	buffer    []byte
	parser    *common.FrameParser
	localAddr netip.Addr
	localPort uint16
	params    Params
	state     *sackTCPState
}

func makeRawConn(ctx context.Context, network string, localAddr netip.Addr) (*ipv4.RawConn, error) {
	if !localAddr.Is4() {
		return nil, fmt.Errorf("makeRawConn only supports IPv4 (for now)")
	}
	lc := net.ListenConfig{
		Control: func(_network, _address string, _c syscall.RawConn) error {
			// TODO apply socket filter here
			return nil
		},
	}
	conn, err := lc.ListenPacket(ctx, network, localAddr.String())
	if err != nil {
		return nil, fmt.Errorf("makeRawConn failed to ListenPacket: %w", err)
	}
	rawConn, err := ipv4.NewRawConn(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("makeRawConn failed to make NewRawConn: %w", err)
	}

	return rawConn, nil
}

func newSackDriver(ctx context.Context, params Params, localAddr netip.Addr) (*sackDriver, error) {
	tcpConn, err := makeRawConn(ctx, "ip:tcp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("newSackDriver failed to make TCP raw conn: %w", err)
	}
	icmpConn, err := makeRawConn(ctx, "ip:icmp", localAddr)
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("newSackDriver failed to make ICMP raw conn: %w", err)
	}

	retval := &sackDriver{
		tcpConn:   tcpConn,
		icmpConn:  icmpConn,
		sendTimes: make([]time.Time, params.ParallelParams.MaxTTL+1),
		buffer:    make([]byte, 1024),
		parser:    common.NewFrameParser(),
		localAddr: localAddr,
		localPort: 0, // to be set by ReadHandshake()
		params:    params,
	}
	return retval, nil
}

func (s *sackDriver) Close() {
	s.tcpConn.Close()
	s.icmpConn.Close()
}

func (s *sackDriver) GetDriverInfo() common.TracerouteDriverInfo {
	return common.TracerouteDriverInfo{
		UsesReceiveICMPProbe: true,
	}
}

func (s *sackDriver) SendProbe(ttl uint8) error {
	if !s.IsHandshakeFinished() {
		return fmt.Errorf("sackDriver hasn't finished ReadHandshake()")
	}
	if ttl < s.params.ParallelParams.MinTTL || ttl > s.params.ParallelParams.MaxTTL {
		return fmt.Errorf("sackDriver asked to send invalid TTL %d", ttl)
	}
	// store the send time for the RTT later when we receive the response
	if !s.sendTimes[ttl].IsZero() {
		return fmt.Errorf("sackDriver asked to send probe for TTL %d but it was already sent", ttl)
	}
	s.sendTimes[ttl] = time.Now()

	gen := sackPacketGen{
		ipPair: s.ExpectedIPPair().Flipped(),
		sPort:  s.localPort,
		dPort:  s.params.Target.Port(),
		state:  *s.state,
	}
	// TODO ipv6
	header, packet, err := gen.generateV4(ttl)
	if err != nil {
		return fmt.Errorf("sackDriver failed to generate packet: %w", err)
	}

	log.TraceFunc(func() string {
		return fmt.Sprintf("sending packet: %+v %s\n", header, hex.EncodeToString(packet))
	})
	err = s.tcpConn.WriteTo(header, packet, nil)
	if err != nil {
		println("error writing packet", err)
		return fmt.Errorf("sackDriver failed to WriteToIP: %w", err)
	}
	return nil
}
func (s *sackDriver) ReceiveProbe(timeout time.Duration) (*common.ProbeResponse, error) {
	return s.receiveProbe(s.tcpConn, timeout)
}
func (s *sackDriver) ReceiveICMPProbe(timeout time.Duration) (*common.ProbeResponse, error) {
	return s.receiveProbe(s.icmpConn, timeout)
}
func (s *sackDriver) receiveProbe(conn *ipv4.RawConn, timeout time.Duration) (*common.ProbeResponse, error) {
	if !s.IsHandshakeFinished() {
		return nil, fmt.Errorf("sackDriver hasn't finished ReadHandshake()")
	}

	err := conn.SetReadDeadline(time.Now().Add(timeout))
	if err != nil {
		return nil, fmt.Errorf("sackDriver failed to SetReadDeadline: %w", err)
	}
	err = s.readAndParse(conn)
	if err != nil {
		return nil, err
	}

	return s.handleProbeLayers()
}

func (s *sackDriver) ExpectedIPPair() common.IPPair {
	// from the target to us
	return common.IPPair{
		SrcAddr: s.params.Target.Addr(),
		DstAddr: s.localAddr,
	}
}

// IsHandshakeFinished returns whether the sackDriver is ready to perform a traceroute.
// After ReadHandshake() succeeds, this returns true.
func (s *sackDriver) IsHandshakeFinished() bool {
	return s.state != nil
}

// getMinSack returns the minimum SACK value from the SACK options.
// we use this to find the earliest TTL that actually arrived
func getMinSack(localInitSeq uint32, opts []layers.TCPOption) (uint32, error) {
	minSack := uint32(math.MaxUint32)
	foundSack := false
	for _, opt := range opts {
		if opt.OptionType != layers.TCPOptionKindSACK {
			continue
		}

		for data := opt.OptionData; len(data) >= 8; data = data[8:] {
			foundSack = true
			leftEdge := binary.BigEndian.Uint32(data[:4])
			relativeLeftEdge := leftEdge - localInitSeq
			if relativeLeftEdge < minSack {
				minSack = relativeLeftEdge
			}
		}
	}
	if !foundSack {
		return 0, fmt.Errorf("sackDriver found no SACK options")
	}
	return minSack, nil
}

func (s *sackDriver) getRTTFromRelSeq(relSeq uint32) (time.Duration, error) {
	if relSeq < uint32(s.params.ParallelParams.MinTTL) || relSeq > uint32(s.params.ParallelParams.MaxTTL) {
		return 0, fmt.Errorf("getRTTFromRelSeq: invalid relative sequence number %d", relSeq)
	}
	if s.sendTimes[relSeq].IsZero() {
		return 0, fmt.Errorf("getRTTFromRelSeq: no probe sent for relative sequence number %d", relSeq)
	}
	return time.Since(s.sendTimes[relSeq]), nil
}

func (s *sackDriver) handleProbeLayers() (*common.ProbeResponse, error) {
	ipPair, err := s.parser.GetIPPair()
	if err != nil {
		return nil, fmt.Errorf("sackDriver failed to get IP pair: %w", err)
	}

	switch s.parser.GetTransportLayer() {
	case layers.LayerTypeTCP:
		if ipPair != s.ExpectedIPPair() {
			return nil, common.ErrReceiveProbeNoPkt
		}
		// make sure the ports match
		if s.params.Target.Port() != uint16(s.parser.TCP.SrcPort) ||
			s.localPort != uint16(s.parser.TCP.DstPort) {
			return nil, common.ErrReceiveProbeNoPkt
		}
		// we only care about selective ACKs
		if s.parser.TCP.SYN || s.parser.TCP.FIN || s.parser.TCP.RST {
			return nil, common.ErrReceiveProbeNoPkt
		}
		// get the first sequence number that was dupe ACKed
		relSeq, err := getMinSack(s.state.localInitSeq, s.parser.TCP.Options)
		if err != nil {
			return nil, &common.BadPacketError{Err: fmt.Errorf("sackDriver failed to get min SACK: %w", err)}
		}
		rtt, err := s.getRTTFromRelSeq(relSeq)
		if err != nil {
			return nil, &common.BadPacketError{Err: fmt.Errorf("sackDriver failed to get RTT: %w", err)}
		}

		return &common.ProbeResponse{
			TTL:    uint8(relSeq),
			IP:     ipPair.SrcAddr,
			RTT:    rtt,
			IsDest: true,
		}, nil
	case layers.LayerTypeICMPv4:
		icmpInfo, err := s.parser.GetICMPInfo()
		if err != nil {
			return nil, &common.BadPacketError{Err: fmt.Errorf("sackDriver failed to get ICMP info: %w", err)}
		}
		if icmpInfo.ICMPType != common.TTLExceeded4 {
			return nil, common.ErrReceiveProbeNoPkt
		}
		tcpInfo, err := common.ParseTCPFirstBytes(icmpInfo.Payload)
		if err != nil {
			return nil, &common.BadPacketError{Err: fmt.Errorf("sackDriver failed to parse TCP info: %w", err)}
		}
		icmpDst := netip.AddrPortFrom(icmpInfo.ICMPPair.DstAddr, tcpInfo.DstPort)
		if icmpDst != s.params.Target {
			log.Tracef("icmp dst mismatch. expected: %s actual: %s", s.params.Target, icmpDst)
			return nil, common.ErrReceiveProbeNoPkt
		}
		if !s.params.LoosenICMPSrc {
			icmpSrc := netip.AddrPortFrom(icmpInfo.IPPair.SrcAddr, tcpInfo.SrcPort)
			expectedSrc := netip.AddrPortFrom(s.localAddr, s.localPort)
			if icmpSrc != expectedSrc {
				log.Tracef("icmp src mismatch. expected: %s actual: %s", expectedSrc, icmpSrc)
				return nil, common.ErrReceiveProbeNoPkt
			}
		}

		relSeq := tcpInfo.Seq - s.state.localInitSeq
		rtt, err := s.getRTTFromRelSeq(relSeq)
		if err != nil {
			return nil, &common.BadPacketError{Err: fmt.Errorf("sackDriver failed to get RTT: %w", err)}
		}
		return &common.ProbeResponse{
			TTL:    uint8(relSeq),
			IP:     ipPair.SrcAddr,
			RTT:    rtt,
			IsDest: false,
		}, nil
	default:
		return nil, common.ErrReceiveProbeNoPkt
	}
}

var _ common.TracerouteDriver = &sackDriver{}

// FakeHandshake is sometimes used when debugging locally, to force the sackDriver to send packets
// even if SACK negotiation would fail
func (s *sackDriver) FakeHandshake() {
	s.localPort = 1234
	s.state = &sackTCPState{
		localInitSeq: 5678,
		localInitAck: 3333,
	}
}

// ReadHandshake polls for a synack from the target and populates the localInitSeq and localInitAck fields.
// it also checks that the target supports SACK.
func (s *sackDriver) ReadHandshake(localPort uint16) error {
	s.localPort = localPort
	// we should have already connected by now so it should be over quickly
	err := s.tcpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if err != nil {
		return fmt.Errorf("sackDriver failed to SetReadDeadline: %w", err)
	}
	for !s.IsHandshakeFinished() {
		err = s.readAndParse(s.tcpConn)

		if common.CheckParallelRetryable("ReadHandshake", err) {
			continue
		} else if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("sackDriver readHandshake timed out")
		} else if err != nil {
			return fmt.Errorf("sackDriver failed to readAndParse: %w", err)
		}

		err = s.handleHandshake()
		if err != nil {
			return fmt.Errorf("sackDriver failed to handleHandshakeLayers: %w", err)
		}
	}
	return nil
}
func (s *sackDriver) handleHandshake() error {
	ipPair, err := s.parser.GetIPPair()
	if err != nil {
		return fmt.Errorf("sackDriver failed to get IP pair: %w", err)
	}

	if s.parser.GetTransportLayer() != layers.LayerTypeTCP {
		return nil
	}

	if ipPair != s.ExpectedIPPair() {
		return nil
	}
	if s.params.Target.Port() != uint16(s.parser.TCP.SrcPort) ||
		s.localPort != uint16(s.parser.TCP.DstPort) {
		log.Debugf("bad ports, %d != %d, %d != %d", s.params.Target.Port(), uint16(s.parser.TCP.SrcPort), s.localPort, uint16(s.parser.TCP.DstPort))
		return nil
	}

	// must be the SYNACK response
	if !s.parser.TCP.SYN || !s.parser.TCP.ACK {
		return nil
	}
	// check if they support SACK otherwise we can't traceroute this way
	foundSackPermitted := false
	state := sackTCPState{}
	for _, opt := range s.parser.TCP.Options {
		log.Tracef("handleHandshake saw option %s", opt.OptionType)
		switch opt.OptionType {
		case layers.TCPOptionKindSACKPermitted:
			foundSackPermitted = true
		case layers.TCPOptionKindTimestamps:
			if len(opt.OptionData) < 8 {
				return fmt.Errorf("sackDriver found truncated timestamps option")
			}
			remoteTSValue := binary.BigEndian.Uint32(opt.OptionData[:4])
			remoteTSEcr := binary.BigEndian.Uint32(opt.OptionData[4:8])

			state.hasTS = true
			// simulate some time passing
			state.tsValue = remoteTSEcr + 50
			// send back their ts value otherwise the connection will be dropped
			state.tsEcr = remoteTSValue
		}
	}
	if !foundSackPermitted {
		return &NotSupportedError{
			Err: fmt.Errorf("SACK not supported by the target %s (missing SACK-permitted option)", s.params.Target),
		}
	}

	// set the localInitSeq and localInitAck based off the response
	state.localInitSeq = s.parser.TCP.Ack - 1
	state.localInitAck = s.parser.TCP.Seq + 1
	s.state = &state
	return nil
}

func (s *sackDriver) readAndParse(conn *ipv4.RawConn) error {
	n, err := conn.Read(s.buffer)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return common.ErrReceiveProbeNoPkt
	}
	if err != nil {
		return fmt.Errorf("sackDriver failed to ReadFromIP: %w", err)
	}
	if n == 0 {
		return common.ErrReceiveProbeNoPkt
	}

	// TODO ipv6
	err = s.parser.ParseIPv4(s.buffer[:n])
	if err != nil {
		log.DebugFunc(func() string {
			return fmt.Sprintf("error parsing packet of length %d: %s, %s", n, err, hex.EncodeToString(s.buffer[:n]))
		})
		return &common.BadPacketError{Err: fmt.Errorf("sackDriver failed to parse packet of length %d: %w", n, err)}
	}

	return nil
}
