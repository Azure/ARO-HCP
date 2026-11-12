// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package probe

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
)

func probeDNS(server, name, protocol string, kind dnsmessage.Type, timeout time.Duration) (result DNSObservation) {
	return probeDNSPort(server, 53, name, protocol, kind, timeout)
}

func probeDNSPort(server string, port int, name, protocol string, kind dnsmessage.Type, timeout time.Duration) (result DNSObservation) {
	start := time.Now()
	result = DNSObservation{Server: server, Name: name, Protocol: protocol, Type: kind.String(), StartedAt: start.UTC(), Stage: "connect"}
	defer func() { result.DurationMS = milliseconds(time.Since(start)) }()
	conn, err := dialSocket(server, "", port, protocol == "udp", start.Add(timeout))
	if err != nil {
		result.Error = err.Error()
		return
	}
	defer capture.CloseWithLog("DNS socket", conn.Close)
	result.Source, result.Destination = conn.LocalAddr().String(), conn.RemoteAddr().String()
	result.Stage = "query"
	var id [2]byte
	if _, err := rand.Read(id[:]); err != nil {
		result.Error = "generate DNS transaction ID"
		return
	}
	questionName, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		result.Error = "invalid DNS question name"
		return
	}
	question := dnsmessage.Question{Name: questionName, Type: kind, Class: dnsmessage.ClassINET}
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: binary.BigEndian.Uint16(id[:]), RecursionDesired: true}, Questions: []dnsmessage.Question{question}}
	wire, err := query.Pack()
	if err != nil {
		result.Error = "encode DNS question"
		return
	}
	if protocol == "tcp" {
		wire = append(binary.BigEndian.AppendUint16(nil, uint16(len(wire))), wire...)
	}
	if _, err := conn.Write(wire); err != nil {
		result.Error = protocolError("DNS query write failed", err)
		return
	}
	result.Stage = "response"
	// Both transports have an explicit response bound. Truncated UDP replies
	// are recorded as such; they never trigger an implicit TCP retry.
	wire = make([]byte, 4097)
	var n int
	if protocol == "tcp" {
		var length [2]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			result.Error = protocolError("DNS frame read failed", err)
			return
		}
		n = int(binary.BigEndian.Uint16(length[:]))
		if n > 4096 {
			result.Truncated = true
			result.Error = "DNS response exceeded 4096 bytes"
			return
		}
		_, err = io.ReadFull(conn, wire[:n])
	} else {
		n, err = conn.Read(wire)
	}
	if err != nil {
		result.Error = protocolError("DNS response read failed", err)
		return
	}
	if n > 4096 {
		result.Truncated = true
		result.Error = "DNS response exceeded 4096 bytes"
		return
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(wire[:n])
	if err != nil || !header.Response || header.ID != query.ID || header.OpCode != 0 {
		result.Error = "invalid or mismatched DNS response"
		return
	}
	questions, err := parser.AllQuestions()
	if err != nil || len(questions) != 1 || questions[0].Type != kind || questions[0].Class != dnsmessage.ClassINET || !strings.EqualFold(questions[0].Name.String(), question.Name.String()) {
		result.Error = "mismatched DNS question"
		return
	}
	result.RCode, result.Truncated = int(header.RCode), header.Truncated
	if header.Truncated {
		result.Error = "DNS response has truncation flag"
		return
	}
	answers, err := parser.AllAnswers()
	if err != nil {
		result.Error = "invalid DNS answers"
		return
	}
	for _, answer := range answers {
		if answer.Header.Type != kind || answer.Header.Class != dnsmessage.ClassINET {
			continue
		}
		if len(result.Answers) == 64 {
			result.Truncated = true
			break
		}
		switch body := answer.Body.(type) {
		case *dnsmessage.AResource:
			result.Answers = append(result.Answers, fmt.Sprintf("%d.%d.%d.%d", body.A[0], body.A[1], body.A[2], body.A[3]))
		case *dnsmessage.AAAAResource:
			result.Answers = append(result.Answers, ip6String(body.AAAA))
		}
	}
	result.Stage = "complete"
	if header.RCode != dnsmessage.RCodeSuccess {
		result.Error = fmt.Sprintf("DNS rcode %d", header.RCode)
	}
	return
}
