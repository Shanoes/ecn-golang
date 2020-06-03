package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/debug"
	"github.com/gopcua/opcua/monitor"
	"github.com/gopcua/opcua/ua"
)

var mqttHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	fmt.Printf("TOPIC: %s\n", msg.Topic())
	fmt.Printf("MSG: %s\n", msg.Payload())
}

func main() {
	var (
		endpoint = flag.String("endpoint", "opc.tcp://localhost:4840", "OPC UA Endpoint URL")
		policy   = flag.String("policy", "", "Security policy: None, Basic128Rsa15, Basic256, Basic256Sha256. Default: auto")
		mode     = flag.String("mode", "", "Security mode: None, Sign, SignAndEncrypt. Default: auto")
		certFile = flag.String("cert", "", "Path to cert.pem. Required for security mode/policy != None")
		keyFile  = flag.String("key", "", "Path to private key.pem. Required for security mode/policy != None")
		nodeIDs  = flag.String("nodes", "", "node ids to subscribe to, seperated by commas")
		nodePre  = flag.String("prefix", "ns=2;s=0:", "prefix to add to Node IDs.")
		interval = flag.String("interval", opcua.DefaultSubscriptionInterval.String(), "subscription interval")
		topic    = flag.String("topic", "ecn/", "The MQTT topic name to publish to")
		broker   = flag.String("broker", "tcp://localhost:1883", "The MQTT broker URI. ex: tcp://10.10.1.1:1883")
		clientID = flag.String("client", "ecn-test", "The MQTT client ID")
		user     = flag.String("user", "cedalo", "The MQTT broker username")
		pass     = flag.String("pass", "xfIxdLKiuQ8Lr45LJFj9WSbgn", "The MQTT broker password")
	)
	flag.BoolVar(&debug.Enable, "debug", false, "enable debug logging")
	flag.Parse()

	// Setup MQTT client with reasonable defaults
	mqtt.DEBUG = log.New(os.Stdout, "", 0)
	mqtt.ERROR = log.New(os.Stdout, "", 0)
	mqttOpts := mqtt.NewClientOptions().AddBroker(*broker).SetClientID(*clientID)
	mqttOpts.SetKeepAlive(2 * time.Second)
	mqttOpts.SetDefaultPublishHandler(mqttHandler)
	mqttOpts.SetPingTimeout(1 * time.Second)
	mqttOpts.SetUsername(*user)
	mqttOpts.SetPassword(*pass)

	mqttClient := mqtt.NewClient(mqttOpts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	// log.SetFlags(0)

	subInterval, err := time.ParseDuration(*interval)
	if err != nil {
		log.Fatal(err)
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-signalCh
		println()
		cancel()
	}()

	endpoints, err := opcua.GetEndpoints(*endpoint)
	if err != nil {
		log.Fatal(err)
	}

	ep := opcua.SelectEndpoint(endpoints, *policy, ua.MessageSecurityModeFromString(*mode))
	if ep == nil {
		log.Fatal("Failed to find suitable endpoint")
	}

	// SRA - for some reason the URL is broken with the default implementaiton
	// so we have to fix it here
	ep.EndpointURL = *endpoint

	log.Print("*", ep.SecurityPolicyURI, ep.SecurityMode)

	opts := []opcua.Option{
		opcua.SecurityPolicy(*policy),
		opcua.SecurityModeString(*mode),
		opcua.CertificateFile(*certFile),
		opcua.PrivateKeyFile(*keyFile),
		opcua.AuthAnonymous(),
		opcua.SecurityFromEndpoint(ep, ua.UserTokenTypeAnonymous),
	}

	c := opcua.NewClient(ep.EndpointURL, opts...)
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	defer c.Close()

	m, err := monitor.NewNodeMonitor(c)
	if err != nil {
		log.Fatal(err)
	}

	m.SetErrorHandler(func(_ *opcua.Client, sub *monitor.Subscription, err error) {
		log.Printf("error: sub=%d err=%s", sub.SubscriptionID(), err.Error())
	})
	wg := &sync.WaitGroup{}

	// Parse nodeIDs and start subscriptions
	split := strings.Split(*nodeIDs, ",")
	var nodes []string
	for _, nodeID := range split {
		nodes = append(nodes, *nodePre+nodeID)
	}
	fmt.Printf("Adding nodes: %q\n", nodes)

	// TODO - I'm not sure of the difference between this..
	// wg.Add(1)
	// go startCallbackSub(ctx, m, subInterval, 0, wg, nodes...)
	// ...and this? Both seem to work.
	wg.Add(1)
	// go startChanSub(ctx, m, subInterval, 0, wg, nodes...)
	go startPubSub(ctx, *topic, mqttClient, m, subInterval, 0, wg, nodes...)

	<-ctx.Done()
	wg.Wait()
}

// subscribe to OPC channel and publish MQTT message on OPC message receipt
func startPubSub(ctx context.Context, topic string, mqttClient mqtt.Client, m *monitor.NodeMonitor, interval, lag time.Duration, wg *sync.WaitGroup, nodes ...string) {
	ch := make(chan *monitor.DataChangeMessage, 64)
	sub, err := m.ChanSubscribe(ctx, &opcua.SubscriptionParameters{Interval: interval}, ch, nodes...)

	if err != nil {
		log.Fatal(err)
	}

	defer cleanup(sub, wg, mqttClient)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			b, e := messageToJSON(*msg)
			if e != nil {
				log.Printf("[channel] " + e.Error())
			} else {
				tag := msg.NodeID.StringID()[2:]
				// log.Printf("[channel] publish " + string(b) + " to " + topic + tag)
				token := mqttClient.Publish(topic+tag, 0, false, b)
				token.Wait()
			}
		}
	}
}

func cleanup(sub *monitor.Subscription, wg *sync.WaitGroup, mqttClient mqtt.Client) {
	log.Printf("stats: sub=%d delivered=%d dropped=%d", sub.SubscriptionID(), sub.Delivered(), sub.Dropped())
	sub.Unsubscribe()
	mqttClient.Disconnect(250)
	wg.Done()
}

type ecnData struct {
	Tag       string
	Val       float32
	Timestamp string
	Err       string
}

func messageToJSON(message monitor.DataChangeMessage) ([]byte, error) {
	var e ecnData
	if message.Error != nil {
		e = ecnData{"", 0.0, "", message.Error.Error()}
	} else {
		e = ecnData{message.NodeID.StringID()[2:], message.Value.Value().(float32), message.SourceTimestamp.UTC().Format(time.StampMilli), ""}
	}
	return json.Marshal(e)
}

// func startCallbackSub(ctx context.Context, m *monitor.NodeMonitor, interval, lag time.Duration, wg *sync.WaitGroup, nodes ...string) {
// 	sub, err := m.Subscribe(
// 		ctx,
// 		&opcua.SubscriptionParameters{
// 			Interval: interval,
// 		},
// 		func(s *monitor.Subscription, msg *monitor.DataChangeMessage) {
// 			if msg.Error != nil {
// 				log.Printf("[callback] sub=%d error=%s", s.SubscriptionID(), msg.Error)
// 			} else {
// 				log.Printf("[callback] sub=%d ts=%s node=%s value=%v", s.SubscriptionID(), msg.SourceTimestamp.UTC().Format(time.RFC3339), msg.NodeID, msg.Value.Value())
// 			}
// 			time.Sleep(lag)
// 		},
// 		nodes...)

// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	defer cleanup(sub, wg)

// 	<-ctx.Done()
// }

// func startChanSub(ctx context.Context, m *monitor.NodeMonitor, interval, lag time.Duration, wg *sync.WaitGroup, nodes ...string) {
// 	ch := make(chan *monitor.DataChangeMessage, 64)
// 	sub, err := m.ChanSubscribe(ctx, &opcua.SubscriptionParameters{Interval: interval}, ch, nodes...)

// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	defer cleanup(sub, wg)

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case msg := <-ch:
// 			if msg.Error != nil {
// 				log.Printf("[channel ] sub=%d error=%s", sub.SubscriptionID(), msg.Error)
// 			} else {
// 				// fmt.Println("---- doing publish ----")
// 				// token := mqttClient.Publish(*topic, byte(*qos), false, *payload)
// 				// token.Wait()

// 				log.Printf("[channel ] sub=%d ts=%s node=%s value=%v", sub.SubscriptionID(), msg.SourceTimestamp.UTC().Format(time.RFC3339), msg.NodeID.StringID(), msg.Value.Value())
// 			}
// 			time.Sleep(lag)
// 		}
// 	}
// }
