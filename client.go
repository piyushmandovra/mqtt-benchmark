package main

import (
	// "encoding/csv"
	"fmt"
	"log"
	// "os"
	"strconv"
	"strings"
	"time"
)

import (
	"github.com/GaryBoone/GoStats/stats"
	"github.com/thanhpk/randstr"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client struct {
	ID         string
	BrokerURL  string
	BrokerUser string
	BrokerPass string
	MsgTopic   string
	MsgSize    int
	MsgCount   int
	Delay	   int
	MsgQoS     byte
	Quiet      bool
	FileName   string
	Folder	   string
}

func (c *Client) Run(res chan *RunResults) {
	newMsgs := make(chan *Message)
	pubMsgs := make(chan *Message)
	doneGen := make(chan bool)
	donePub := make(chan bool)
	runResults := new(RunResults)
	pubData := [][]string{}
	// brokerID := c.BrokerURL

	started := time.Now()
	// start generator
	go c.genMessages(newMsgs, doneGen)
	// start publisher
	go c.pubMessages(newMsgs, pubMsgs, doneGen, donePub)

	// runResults.ID = c.ID
	times := []float64{}
	for {
		select {
		case m := <-pubMsgs:
			if m.Error {
				log.Printf("CLIENT %v ERROR publishing message: %v: at %v\n", c.ID, m.Topic, m.Sent.Unix())
				runResults.Failures++
			} else {
				if !c.Quiet {
				log.Printf("%v) Message published: %v: sent: %v delivered: %v flight time: %v\n", runResults.Successes, m.Topic, m.Sent, m.Delivered, m.Delivered.Sub(m.Sent))
				}
				runResults.Successes++
				times = append(times, m.Delivered.Sub(m.Sent).Seconds()*1000) // in milliseconds
				pubData = append(pubData, []string{fmt.Sprintf("%v", c.ID),
								   fmt.Sprintf("%v", m.Sent.Format("2006-01-02T15:04:05.000000000")), 
								   fmt.Sprintf("%v", m.Delivered.Format("2006-01-02T15:04:05.000000000")),
								   fmt.Sprintf("%v", m.Delivered.Sub(m.Sent).Seconds()*1000),
								   fmt.Sprintf("%v", c.MsgQoS)})			
			}
		case <-donePub:
			// calculate results
			duration := time.Now().Sub(started)
			runResults.MsgTimeMin = stats.StatsMin(times)
			runResults.MsgTimeMax = stats.StatsMax(times)
			runResults.MsgTimeMean = stats.StatsMean(times)
			runResults.RunTime = duration.Seconds()
			runResults.MsgsPerSec = float64(runResults.Successes) / duration.Seconds()
			// calculate std if sample is > 1, otherwise leave as 0 (convention)
			if c.MsgCount > 1 {
				runResults.MsgTimeStd = stats.StatsSampleStandardDeviation(times)
			}

			// 	//remove tcp:// remove port (after :)

			// //create file
			// os.MkdirAll(fmt.Sprintf("%v/raw/", c.Folder), os.ModePerm)
			
			// _file, err := os.Create(fmt.Sprintf("%v/raw/b%v_rawC%v_pub_%v.csv", c.Folder, brokerID, c.ID, c.FileName))
			// checkError("Cannot create file", err)
			// defer _file.Close()

			// _writer := csv.NewWriter(_file)
			// defer _writer.Flush()

			// //write to file
			// for _, value := range pubData {
        	// 		err := _writer.Write(value)
			// 	checkError("Cannot write to file", err)
			// }	

			// report results and exit
			res <- runResults
			return
		}
	}
}

func (c *Client) genMessages(ch chan *Message, done chan bool) {
	for i := 0; i < c.MsgCount; i++ {
		ch <- &Message{
			Topic:   c.MsgTopic,
			QoS:     c.MsgQoS,
			Payload: strconv.FormatInt(time.Now().UnixNano()/1000000, 10),
		}
	}
	done <- true
	// log.Printf("CLIENT %v is done generating messages\n", c.ID)
	return
}

func (c *Client) pubMessages(in, out chan *Message, doneGen, donePub chan bool) {
	onConnected := func(client mqtt.Client) {
		if !c.Quiet {
			log.Printf("CLIENT %v is connected to the broker %v\n", c.ID, c.BrokerURL)
		}

		BrokerIP := c.BrokerURL
		if strings.Contains(c.BrokerURL, ".") {
		//remove tcp:// remove port (after :)
			BrokerIP = strings.Trim(strings.Split(c.BrokerURL, ":")[1], "//")
		} else {
			BrokerIP = "local"	
		}
		
		ctr := 0
		for {
			select {
			case m := <-in:
				m.Sent = time.Now()
				msg := randstr.String(c.MsgSize)
				payload := fmt.Sprintf("%v,%v,%v,%v,%v", BrokerIP, c.ID, strconv.FormatInt(time.Now().UnixNano()/1000000, 10), ctr, msg)
				token := client.Publish(c.MsgTopic, m.QoS, false, payload)
				token.Wait()
				if token.Error() != nil {
					log.Printf("CLIENT %v Error sending message: %v\n", c.ID, token.Error())
					m.Error = true
				} else {
					m.Delivered = time.Now()
					m.Error = false
				}
				out <- m
				time.Sleep(time.Duration(c.Delay) * time.Millisecond)	
				if ctr > 0 && ctr%100 == 0 {
					if !c.Quiet {
						log.Printf("CLIENT %v published %v messages and keeps publishing...\n", c.ID, ctr)
					}
				}
				ctr++
			case <-doneGen:
				donePub <- true
				if !c.Quiet {
					log.Printf("CLIENT %v is done publishing\n", c.ID)
				}
				return
			}
		}
	}

	opts := mqtt.NewClientOptions().
		AddBroker(c.BrokerURL).
		SetClientID(fmt.Sprintf("mqtt-benchmark-%v-%v", time.Now().Format(time.RFC3339Nano), c.ID)).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetOnConnectHandler(onConnected).
		SetConnectionLostHandler(func(client mqtt.Client, reason error) {
			log.Printf("CLIENT %v lost connection to the broker: %v. Will reconnect...\n", c.ID, reason.Error())
		})
	if c.BrokerUser != "" && c.BrokerPass != "" {
		opts.SetUsername(c.BrokerUser)
		opts.SetPassword(c.BrokerPass)
	}
	client := mqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()

	if token.Error() != nil {
		log.Printf("CLIENT %v had error connecting to the broker: %v\n", c.ID, token.Error())
	}
}
