/*
 * Copyright 2014 VMware, Inc.  All rights reserved.  Licensed under the Apache v2 License.
 */

package govcloudair

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log"
	"net/url"
	"os"

	types2 "github.com/emccode/govcloudair/types/v56"
	types "github.com/vmware/govcloudair/types/v56"
)

type EdgeGateway struct {
	EdgeGateway *types.EdgeGateway
	c           *Client
}

func NewEdgeGateway(c *Client) *EdgeGateway {
	return &EdgeGateway{
		EdgeGateway: new(types.EdgeGateway),
		c:           c,
	}
}

func (e *EdgeGateway) Refresh() error {

	if e.EdgeGateway == nil {
		return fmt.Errorf("cannot refresh, Object is empty")
	}

	u, _ := url.ParseRequestURI(e.EdgeGateway.HREF)

	req := e.c.NewRequest(map[string]string{}, "GET", *u, nil)

	resp, err := checkResp(e.c.Http.Do(req))
	if err != nil {
		return fmt.Errorf("error retreiving Edge Gateway: %s", err)
	}

	// Empty struct before a new unmarshal, otherwise we end up with duplicate
	// elements in slices.
	e.EdgeGateway = &types.EdgeGateway{}

	if err = decodeBody(resp, e.EdgeGateway); err != nil {
		return fmt.Errorf("error decoding Edge Gateway response: %s", err)
	}

	// The request was successful
	return nil
}

func (e *EdgeGateway) Remove1to1Mapping(internal, external string) (Task, error) {

	// Refresh EdgeGateway rules
	err := e.Refresh()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	var uplinkif string
	for _, gifs := range e.EdgeGateway.Configuration.GatewayInterfaces.GatewayInterface {
		if gifs.InterfaceType == "uplink" {
			uplinkif = gifs.Network.HREF
		}
	}

	newedgeconfig := e.EdgeGateway.Configuration.EdgeGatewayServiceConfiguration

	// Take care of the NAT service
	newnatservice := &types.NatService{}

	// Copy over the NAT configuration
	newnatservice.IsEnabled = newedgeconfig.NatService.IsEnabled
	newnatservice.NatType = newedgeconfig.NatService.NatType
	newnatservice.Policy = newedgeconfig.NatService.Policy
	newnatservice.ExternalIP = newedgeconfig.NatService.ExternalIP

	for k, v := range newedgeconfig.NatService.NatRule {

		// Kludgy IF to avoid deleting DNAT rules not created by us.
		// If matches, let's skip it and continue the loop
		if v.RuleType == "DNAT" &&
			v.GatewayNatRule.OriginalIP == external &&
			v.GatewayNatRule.TranslatedIP == internal &&
			v.GatewayNatRule.OriginalPort == "any" &&
			v.GatewayNatRule.TranslatedPort == "any" &&
			v.GatewayNatRule.Protocol == "any" &&
			v.GatewayNatRule.Interface.HREF == uplinkif {
			continue
		}

		// Kludgy IF to avoid deleting SNAT rules not created by us.
		// If matches, let's skip it and continue the loop
		if v.RuleType == "SNAT" &&
			v.GatewayNatRule.OriginalIP == internal &&
			v.GatewayNatRule.TranslatedIP == external &&
			v.GatewayNatRule.Interface.HREF == uplinkif {
			continue
		}

		// If doesn't match the above IFs, it's something we need to preserve,
		// let's add it to the new NatService struct
		newnatservice.NatRule = append(newnatservice.NatRule, newedgeconfig.NatService.NatRule[k])

	}

	// Fill the new NatService Section
	newedgeconfig.NatService = newnatservice

	// Take care of the Firewall service
	newfwservice := &types.FirewallService{}

	// Copy over the firewall configuration
	newfwservice.IsEnabled = newedgeconfig.FirewallService.IsEnabled
	newfwservice.DefaultAction = newedgeconfig.FirewallService.DefaultAction
	newfwservice.LogDefaultAction = newedgeconfig.FirewallService.LogDefaultAction

	for k, v := range newedgeconfig.FirewallService.FirewallRule {

		// Kludgy IF to avoid deleting inbound FW rules not created by us.
		// If matches, let's skip it and continue the loop
		if v.Policy == "allow" &&
			v.Protocols.Any == true &&
			v.DestinationPortRange == "Any" &&
			v.SourcePortRange == "Any" &&
			v.SourceIP == "Any" &&
			v.DestinationIP == external {
			continue
		}

		// Kludgy IF to avoid deleting outbound FW rules not created by us.
		// If matches, let's skip it and continue the loop
		if v.Policy == "allow" &&
			v.Protocols.Any == true &&
			v.DestinationPortRange == "Any" &&
			v.SourcePortRange == "Any" &&
			v.SourceIP == internal &&
			v.DestinationIP == "Any" {
			continue
		}

		// If doesn't match the above IFs, it's something we need to preserve,
		// let's add it to the new FirewallService struct
		newfwservice.FirewallRule = append(newfwservice.FirewallRule, newedgeconfig.FirewallService.FirewallRule[k])

	}

	// Fill the new FirewallService Section
	newedgeconfig.FirewallService = newfwservice

	// Fix
	newedgeconfig.NatService.IsEnabled = true

	output, err := xml.MarshalIndent(newedgeconfig, "  ", "    ")
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	debug := os.Getenv("GOVCLOUDAIR_DEBUG")

	if debug == "true" {
		fmt.Printf("\n\nXML DEBUG: %s\n\n", string(output))
	}

	b := bytes.NewBufferString(xml.Header + string(output))

	s, _ := url.ParseRequestURI(e.EdgeGateway.HREF)
	s.Path += "/action/configureServices"

	req := e.c.NewRequest(map[string]string{}, "POST", *s, b)

	req.Header.Add("Content-Type", "application/vnd.vmware.admin.edgeGatewayServiceConfiguration+xml")

	resp, err := checkResp(e.c.Http.Do(req))
	if err != nil {
		return Task{}, fmt.Errorf("error reconfiguring Edge Gateway: %s", err)
	}

	task := NewTask(e.c)

	if err = decodeBody(resp, task.Task); err != nil {
		return Task{}, fmt.Errorf("error decoding Task response: %s", err)
	}

	// The request was successful
	return *task, nil

}

func (e *EdgeGateway) Create1to1Mapping(internal string, external string, description string, inFirewallAny bool, outFirewallAny bool) (Task, error) {

	// Refresh EdgeGateway rules
	err := e.Refresh()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	var uplinkif string
	for _, gifs := range e.EdgeGateway.Configuration.GatewayInterfaces.GatewayInterface {
		if gifs.InterfaceType == "uplink" {
			uplinkif = gifs.Network.HREF
		}
	}

	newedgeconfig := e.EdgeGateway.Configuration.EdgeGatewayServiceConfiguration

	if newedgeconfig.NatService == nil {
		newedgeconfig.NatService = &types.NatService{}

	}

	snat := &types.NatRule{
		Description: description,
		RuleType:    "SNAT",
		IsEnabled:   true,
		GatewayNatRule: &types.GatewayNatRule{
			Interface: &types.Reference{
				HREF: uplinkif,
			},
			OriginalIP:   internal,
			TranslatedIP: external,
			Protocol:     "any",
		},
	}

	newedgeconfig.NatService.NatRule = append(newedgeconfig.NatService.NatRule, snat)

	dnat := &types.NatRule{
		Description: description,
		RuleType:    "DNAT",
		IsEnabled:   true,
		GatewayNatRule: &types.GatewayNatRule{
			Interface: &types.Reference{
				HREF: uplinkif,
			},
			OriginalIP:     external,
			OriginalPort:   "any",
			TranslatedIP:   internal,
			TranslatedPort: "any",
			Protocol:       "any",
		},
	}

	newedgeconfig.NatService.NatRule = append(newedgeconfig.NatService.NatRule, dnat)

	fwin := &types.FirewallRule{}
	if inFirewallAny {
		fwin = &types.FirewallRule{
			Description: description,
			IsEnabled:   true,
			Policy:      "allow",
			Protocols: &types.FirewallRuleProtocols{
				Any: true,
			},
			DestinationPortRange: "Any",
			DestinationIP:        external,
			SourcePortRange:      "Any",
			SourceIP:             "Any",
			EnableLogging:        false,
		}
		newedgeconfig.FirewallService.FirewallRule = append(newedgeconfig.FirewallService.FirewallRule, fwin)
	}

	fwout := &types.FirewallRule{}
	if outFirewallAny {
		fwout = &types.FirewallRule{
			Description: description,
			IsEnabled:   true,
			Policy:      "allow",
			Protocols: &types.FirewallRuleProtocols{
				Any: true,
			},
			DestinationPortRange: "Any",
			DestinationIP:        "Any",
			SourcePortRange:      "Any",
			SourceIP:             internal,
			EnableLogging:        false,
		}
		newedgeconfig.FirewallService.FirewallRule = append(newedgeconfig.FirewallService.FirewallRule, fwout)
	}

	output, err := xml.MarshalIndent(newedgeconfig, "  ", "    ")
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	debug := os.Getenv("GOVCLOUDAIR_DEBUG")

	if debug == "true" {
		fmt.Printf("\n\nXML DEBUG: %s\n\n", string(output))
	}

	b := bytes.NewBufferString(xml.Header + string(output))

	s, _ := url.ParseRequestURI(e.EdgeGateway.HREF)
	s.Path += "/action/configureServices"

	req := e.c.NewRequest(map[string]string{}, "POST", *s, b)

	req.Header.Add("Content-Type", "application/vnd.vmware.admin.edgeGatewayServiceConfiguration+xml")

	resp, err := checkResp(e.c.Http.Do(req))
	if err != nil {
		return Task{}, fmt.Errorf("error reconfiguring Edge Gateway: %s", err)
	}

	task := NewTask(e.c)

	if err = decodeBody(resp, task.Task); err != nil {
		return Task{}, fmt.Errorf("error decoding Task response: %s", err)
	}

	// The request was successful
	return *task, nil

}

func (e *EdgeGateway) UpdateFirewall(newedgeconfig *types.GatewayFeatures) (Task, error) {
	// Refresh EdgeGateway rules
	err := e.Refresh()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	//newedgeconfig := e.EdgeGateway.Configuration.EdgeGatewayServiceConfiguration

	output, err := xml.MarshalIndent(newedgeconfig, "  ", "    ")
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	debug := os.Getenv("GOVCLOUDAIR_DEBUG")

	if debug == "true" {
		fmt.Printf("\n\nXML DEBUG: %s\n\n", string(output))
	}

	b := bytes.NewBufferString(xml.Header + string(output))

	s, _ := url.ParseRequestURI(e.EdgeGateway.HREF)
	s.Path += "/action/configureServices"

	req := e.c.NewRequest(map[string]string{}, "POST", *s, b)

	req.Header.Add("Content-Type", "application/vnd.vmware.admin.edgeGatewayServiceConfiguration+xml")

	resp, err := checkResp(e.c.Http.Do(req))
	if err != nil {
		return Task{}, fmt.Errorf("error reconfiguring Edge Gateway: %s", err)
	}

	task := NewTask(e.c)

	if err = decodeBody(resp, task.Task); err != nil {
		return Task{}, fmt.Errorf("error decoding Task response: %s", err)
	}

	// The request was successful
	return *task, nil
}

func (e *EdgeGateway) RequestPublicIP(networkname string, publicipcount string) (Task, error) {

	// Refresh EdgeGateway rules
	err := e.Refresh()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	gatewayInterfaces := e.EdgeGateway.Configuration.GatewayInterfaces.GatewayInterface

	network := &types.Reference{}
	for _, gatewayInterface := range gatewayInterfaces {
		if gatewayInterface.Name == networkname {
			network = gatewayInterface.Network
			break
		}
	}

	if network == nil {
		log.Fatalf("err: couldn't find network name")
	}

	externalIPAddressActionList := &types2.ExternalIpAddressActionList{}
	externalIPAddressActionList.Xmlns = "http://www.vmware.com/vcloud/networkservice/1.0"
	externalIPAddressActionList.Allocation = &types2.Allocation{
		ExternalNetworkName:                   network.Name,
		ExternalNetworkRef:                    network.HREF,
		NumberOfExternalIPAddressesToAllocate: publicipcount,
	}

	output, err := xml.MarshalIndent(externalIPAddressActionList, "  ", "    ")
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	debug := os.Getenv("GOVCLOUDAIR_DEBUG")

	if debug == "true" {
		fmt.Printf("\n\nXML DEBUG: %s\n\n", string(output))
	}

	b := bytes.NewBufferString(xml.Header + string(output))

	s, _ := url.ParseRequestURI(e.EdgeGateway.HREF)
	s.Path += "/action/manageExternalIpAddresses"

	req := e.c.NewRequest(map[string]string{}, "PUT", *s, b)
	req.Header.Add("Accept", "application/xml;version=5.7")
	req.Header.Add("Content-Type", "application/vnd.vmware.vchs.edgeGatewayIpAllocation.list+xml")

	resp, err := checkResp(e.c.Http.Do(req))
	if err != nil {
		return Task{}, fmt.Errorf("error reconfiguring Edge Gateway: %s", err)
	}

	task := NewTask(e.c)

	if err = decodeBody(resp, task.Task); err != nil {
		return Task{}, fmt.Errorf("error decoding Task response: %s", err)
	}

	// The request was successful
	return *task, nil

}

func (e *EdgeGateway) RemovePublicIP(networkname string, publicip string) (Task, error) {

	// Refresh EdgeGateway rules
	err := e.Refresh()
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	gatewayInterfaces := e.EdgeGateway.Configuration.GatewayInterfaces.GatewayInterface

	network := &types.Reference{}
	for _, gatewayInterface := range gatewayInterfaces {
		if gatewayInterface.Name == networkname {
			network = gatewayInterface.Network
			break
		}
	}

	if network == nil {
		log.Fatalf("err: couldn't find network name")
	}

	externalIPAddressActionList := &types2.ExternalIpAddressActionList{}
	externalIPAddressActionList.Xmlns = "http://www.vmware.com/vcloud/networkservice/1.0"
	externalIPAddressActionList.Deallocation = &types2.Deallocation{
		ExternalNetworkName: network.Name,
		ExternalNetworkRef:  network.HREF,
		ExternalIPAddress:   publicip,
	}

	output, err := xml.MarshalIndent(externalIPAddressActionList, "  ", "    ")
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}

	debug := os.Getenv("GOVCLOUDAIR_DEBUG")

	if debug == "true" {
		fmt.Printf("\n\nXML DEBUG: %s\n\n", string(output))
	}

	b := bytes.NewBufferString(xml.Header + string(output))

	s, _ := url.ParseRequestURI(e.EdgeGateway.HREF)
	s.Path += "/action/manageExternalIpAddresses"

	req := e.c.NewRequest(map[string]string{}, "PUT", *s, b)
	req.Header.Add("Accept", "application/xml;version=5.7")
	req.Header.Add("Content-Type", "application/vnd.vmware.vchs.edgeGatewayIpAllocation.list+xml")

	resp, err := checkResp(e.c.Http.Do(req))
	if err != nil {
		return Task{}, fmt.Errorf("error reconfiguring Edge Gateway: %s", err)
	}

	task := NewTask(e.c)

	if err = decodeBody(resp, task.Task); err != nil {
		return Task{}, fmt.Errorf("error decoding Task response: %s", err)
	}

	// The request was successful
	return *task, nil

}
