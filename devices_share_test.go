package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestDeviceDiscoveryNeverProbesShareNodes(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		for _, listingFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("admitted=%t/listingFails=%t", admitted, listingFails), func(t *testing.T) {
				device := catalogDevice("share-node", "public-share", "100.64.0.10")
				device.Tags = []string{"tag:app", shareTag}
				listed := &fakeDeviceLister{devices: []Device{device}}
				if listingFails {
					listed.err = errors.New("netmap unavailable")
				}
				c := &Catalog{
					DNSSuffix: "ts.test", Devices: listed, DeviceTags: []string{"tag:app"},
					InsecureTLSTags: []string{shareTag}, PreviewPorts: []uint16{8443},
					DeviceProber: &DeviceProber{dial: func(context.Context, string, string) (net.Conn, error) {
						t.Error("share node was probed")
						return nil, errors.New("forbidden")
					}},
				}
				if admitted {
					c.admitted = map[endpointID]Device{}
					c.endpoints = map[endpointID]*endpointState{}
					for _, port := range []uint16{443, 8443} {
						id := endpointID{device.ID, port}
						c.admitted[id] = device
						c.endpoints[id] = &endpointState{dnsName: device.DNSName, tlsUp: true}
					}
				}
				entries, _, err := c.refreshDevices(t.Context(), map[string]bool{}, map[string]Override{}, true)
				if (err != nil) != listingFails || len(entries) != 0 || len(c.admitted) != 0 || len(c.endpoints) != 0 {
					t.Fatal("share node entered device discovery")
				}
				app := catalogDevice("app", "ordinary-app", "100.64.0.11")
				app.Tags = []string{"tag:app"}
				eligible, _, _ := c.eligibleDevices([]Device{device, app}, map[string]bool{}, map[string]Override{})
				if len(eligible) != 2 {
					t.Fatal("ordinary HTTPS/preview endpoints were hidden")
				}
				for id := range eligible {
					if id.node != app.ID {
						t.Fatal("share node remained eligible")
					}
				}
			})
		}
	}
}
