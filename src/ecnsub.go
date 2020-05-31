// Copyright 2018-2020 opcua authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/debug"
	"github.com/gopcua/opcua/ua"
)

func main() {
	var (
		endpoint = flag.String("endpoint", "opc.tcp://localhost:4840", "OPC UA Endpoint URL")
		policy   = flag.String("policy", "", "Security policy: None, Basic128Rsa15, Basic256, Basic256Sha256. Default: auto")
		mode     = flag.String("mode", "", "Security mode: None, Sign, SignAndEncrypt. Default: auto")
		certFile = flag.String("cert", "", "Path to cert.pem. Required for security mode/policy != None")
		keyFile  = flag.String("key", "", "Path to private key.pem. Required for security mode/policy != None")
		//nodeID   = flag.String("node", "", "node id to subscribe to")
		nodeIDs  = flag.String("nodes", "", "node ids to subscribe to, seperated by commas")
		nodePre  = flag.String("prefix", "ns=2;s=0:", "prefix to add to Node IDs.")
		interval = flag.String("interval", opcua.DefaultSubscriptionInterval.String(), "subscription interval")
	)

	flag.BoolVar(&debug.Enable, "debug", false, "enable debug logging")
	flag.Parse()
	log.SetFlags(0)

	subInterval, err := time.ParseDuration(*interval)
	if err != nil {
		log.Fatal(err)
	}

	// add an arbitrary timeout to demonstrate how to stop a subscription
	// with a context.
	d := 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()

	//signalCh := make(chan os.Signal, 1)
	//signal.Notify(signalCh, os.Interrupt)

	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()

	//go func() {
	//	<-signalCh
	//	println()
	//	cancel()
	//}()

	endpoints, err := opcua.GetEndpoints(*endpoint)
	if err != nil {
		log.Fatal(err)
	}

	ep := opcua.SelectEndpoint(endpoints, *policy, ua.MessageSecurityModeFromString(*mode))
	if ep == nil {
		log.Fatal("Failed to find suitable endpoint")
	}

	fmt.Println("*", ep.SecurityPolicyURI, ep.SecurityMode)

	opts := []opcua.Option{
		opcua.SecurityPolicy(*policy),
		opcua.SecurityModeString(*mode),
		opcua.CertificateFile(*certFile),
		opcua.PrivateKeyFile(*keyFile),
		opcua.AuthAnonymous(),
		opcua.SecurityFromEndpoint(ep, ua.UserTokenTypeAnonymous),
	}

	fmt.Println("EP ", ep.EndpointURL)

	// SRA - for some reason the URL is broken with the default implementaiton
	// so we have to fix it here
	ep.EndpointURL = *endpoint

	c := opcua.NewClient(ep.EndpointURL, opts...)
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	notifyCh := make(chan *opcua.PublishNotificationData)

	sub, err := c.Subscribe(&opcua.SubscriptionParameters{
		Interval: subInterval,
	}, notifyCh)
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Cancel()
	log.Printf("Created subscription with id %v", sub.SubscriptionID)

	split := strings.Split(*nodeIDs, ",")
	fmt.Printf("Node split result: %q\n", split)
	for _, nodeID := range split {
		fmt.Printf("Creating node: %q\n", *nodePre+nodeID)
	}
	// 	id, err := ua.ParseNodeID(nodeID)
	// 	if err != nil {
	// 		log.Fatal(err)
	// 	}
	// 	// arbitrary client handle for the monitoring item
	// 	fmt.Printf("Creating monitor: %q\n", *nodePre+nodeID)
	// 	handle := uint32(42 + index)
	// 	miCreateRequest := opcua.NewMonitoredItemCreateRequestWithDefaults(id, ua.AttributeIDValue, handle)
	// 	res, err := sub.Monitor(ua.TimestampsToReturnBoth, miCreateRequest)
	// 	if err != nil || res.Results[0].StatusCode != ua.StatusOK {
	// 		log.Fatal(err)
	// 	}
	// }

	id, err := ua.ParseNodeID("ns=2;s=0:TANK1_LEVEL")
	if err != nil {
		log.Fatal(err)
	}

	// arbitrary client handle for the monitoring item
	handle := uint32(42)
	miCreateRequest := opcua.NewMonitoredItemCreateRequestWithDefaults(id, ua.AttributeIDValue, handle)
	res, err := sub.Monitor(ua.TimestampsToReturnBoth, miCreateRequest)
	if err != nil || res.Results[0].StatusCode != ua.StatusOK {
		log.Fatal(err)
	}

	go sub.Run(ctx) // start Publish loop

	// read from subscription's notification channel until ctx is cancelled
	for {
		select {
		case <-ctx.Done():
			return
		case res := <-notifyCh:
			if res.Error != nil {
				log.Print(res.Error)
				continue
			}

			switch x := res.Value.(type) {
			case *ua.DataChangeNotification:
				for _, item := range x.MonitoredItems {
					data := item.Value.Value.Value()
					log.Printf("MonitoredItem with client handle %v = %v", item.ClientHandle, data)
				}

			default:
				log.Printf("what's this publish result? %T", res.Value)
			}
		}
	}
}
