package summary_test

import (
	"strings"
	"testing"

	"logauditorgo/internal/summary"
)

func TestSummaryEngine(t *testing.T) {
	tests := []struct {
		name     string
		module   string
		brief    string
		severity int
		rawMsg   string
		params   map[string]string
		contains []string
	}{
		{
			name:     "BGP Down with remote peer and reason",
			module:   "BGP",
			brief:    "BGP_STATE_BACKWARD",
			severity: 2,
			params: map[string]string{
				"bgpPeerRemoteAddr": "192.168.10.1",
				"bgpPeerLastState":  "Established",
				"reason":            "HoldTimer expired",
			},
			contains: []string{"BGP邻居中断", "192.168.10.1", "HoldTimer expired"},
		},
		{
			name:     "BGP Up with PeerID",
			module:   "BGP",
			brief:    "BGP_STATE_ESTABLISHED",
			severity: 6,
			params: map[string]string{
				"PeerID": "10.1.1.2",
			},
			contains: []string{"BGP邻居建立", "10.1.1.2", "ESTABLISHED"},
		},
		{
			name:     "OSPF Down",
			module:   "OSPF",
			brief:    "NBR_DOWN_REASON",
			severity: 2,
			params: map[string]string{
				"RouterID":      "1.1.1.1",
				"InterfaceName": "GigabitEthernet0/0/1",
				"Reason":        "Physical link down",
			},
			contains: []string{"OSPF邻居中断", "GigabitEthernet0/0/1", "1.1.1.1", "Physical link down"},
		},
		{
			name:     "BFD Down",
			module:   "BFD",
			brief:    "BFD_SESS_DOWN",
			severity: 2,
			params: map[string]string{
				"PeerAddr": "10.2.2.2",
				"Diag":     "Echo Failed",
			},
			contains: []string{"BFD会话中断", "10.2.2.2", "Echo Failed"},
		},
		{
			name:     "IFNET Link Down",
			module:   "IFNET",
			brief:    "IF_DOWN",
			severity: 2,
			params: map[string]string{
				"InterfaceName": "Eth-Trunk1",
				"Reason":        "Line protocol DOWN",
			},
			contains: []string{"接口链路中断", "Eth-Trunk1", "Line protocol DOWN"},
		},
		{
			name:     "EVPN Mac Duplication Alarm (Real Log Scenario)",
			module:   "EVPN",
			brief:    "hwEvpnMacDupVpnAlarm_active",
			severity: 2,
			params: map[string]string{
				"EVPNInstanceName": "9",
				"MAC":              "02bf-0a09-09fa",
				"InterfaceName1":   "Eth-Trunk9.25",
				"IPAddress1":       "10.0.4.188",
			},
			contains: []string{"EVPN MAC重复告警", "实例[9]", "MAC[02bf-0a09-09fa]", "Eth-Trunk9.25", "10.0.4.188"},
		},
		{
			name:     "CLI CMDRECORD (Real Log Scenario)",
			module:   "CLI",
			brief:    "CMDRECORD",
			severity: 5,
			params: map[string]string{
				"Task":    "VTY1",
				"Ip":      "10.17.11.26",
				"User":    "admin",
				"Command": "display clock",
			},
			contains: []string{"CLI操作记录", "admin", "10.17.11.26", "display clock"},
		},
		{
			name:     "Unknown Module Fallback with parameters",
			module:   "XYZ_CUSTOM",
			brief:    "TEST_ALERT",
			severity: 4,
			params: map[string]string{
				"KeyA": "ValA",
				"KeyB": "ValB",
			},
			contains: []string{"[XYZ_CUSTOM/TEST_ALERT]", "KeyA: ValA"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summary.GenerateSummary(tt.module, tt.brief, tt.severity, tt.rawMsg, tt.params)
			for _, exp := range tt.contains {
				if !strings.Contains(got, exp) {
					t.Errorf("summary for %s = '%s', expected to contain '%s'", tt.name, got, exp)
				}
			}
		})
	}
}
