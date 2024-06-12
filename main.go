package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/namsral/flag"
	log "github.com/sirupsen/logrus"
)

var (
	dns        string
	hostedZone string
	dnsTTL     int
	ipAddress  string
	setupDelay int

	gracefulStop = make(chan os.Signal, 1)
	sess         = session.Must(session.NewSession())
)

func configureFromFlags() {
	flag.StringVar(&dns, "dns", "my.example.com", "DNS name to register in Route53")
	flag.StringVar(&hostedZone, "hostedzone", "Z2AAAABCDEFGT4", "Hosted zone ID in route53")
	flag.IntVar(&dnsTTL, "dnsttl", 10, "Timeout for DNS entry")
	flag.StringVar(&ipAddress, "ipaddress", "public-ipv4", "IP Address for A Record")
	flag.IntVar(&dnsTTL, "setupdelay", 10, "Wait time before setting up DNS (in seconds)")
	flag.Parse()

	if ipAddress == "public-ipv4" {
		log.Infof("Fetching IP Address from EC2 public-ipv4")
		metadata := ec2metadata.New(sess)
		publicIpv4, err := metadata.GetMetadata("public-ipv4")
		if err != nil {
			log.Fatalf("Failed to fetch IPV4 public IP: %v", err)
		}
		ipAddress = publicIpv4
	} else if ipAddress == "ecs" {
		log.Infof("Fetching IP Address from ECS metadata")
		metadata, err := getEcsMetadata()
		if err != nil {
			log.Fatalf("Failed to fetch ECS metadata: %v", err)
		}
		ipAddress = metadata.Networks[0].IPv4Addresses[0] // use the first IP address
	}
}

func dumpConfig() {
	log.Infof("DNS=%v", dns)
	log.Infof("DNSTTL=%v", dnsTTL)
	log.Infof("HOSTEDZONE=%v", hostedZone)
	log.Infof("IPADDRESS=%v", ipAddress)
	log.Infof("SETUPDELAY=%v", setupDelay)
}

func catchSignals() {
	sig := <-gracefulStop
	log.Infof("Caught Signal: %+v", sig)

	tearDownDNS()
}

func tearDownDNS() {
	log.Infof("Tearing down Route 53 DNS Name A %s => %s", dns, ipAddress)
	svc := route53.New(sess)
	input := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String("DELETE"),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(dns),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String(ipAddress),
							},
						},
						TTL:           aws.Int64(int64(dnsTTL)),
						Type:          aws.String("A"),
						Weight:        aws.Int64(100),
						SetIdentifier: aws.String(ipAddress),
					},
				},
			},
		},
		HostedZoneId: aws.String(hostedZone),
	}

	changeSet, err := svc.ChangeResourceRecordSets(input)

	if err != nil {
		log.Fatalf("Failed to delete DNS, exiting: %v", err.Error())
	}

	log.Info("Request sent to Route 53...")
	waitForSync(changeSet)

	// Then wait the DNS Timeout to expire
	log.Infof("Waiting for DNS Timeout to expire (%d seconds)", dnsTTL)
	time.Sleep(time.Duration(dnsTTL) * time.Second)
	log.Info("DNS Timeout expiry finished")
	log.Exit(0)
}

func setupDNS() {
	log.Infof("Setting up Route 53 DNS Name A %s => %s", dns, ipAddress)

	// Wait for setupDelay, also check for signals during this period
	if setupDelay > 0 {
		log.Infof("Waiting %d seconds before setting up DNS (SETUPDELAY)", setupDelay)
		for i := 0; i < setupDelay; i++ {
			select {
			case sig := <-gracefulStop:
				log.Fatalf("Caught Signal during SETUPDELAY period: %+v", sig)
			default:
				time.Sleep(1 * time.Second)
			}
		}
		log.Info("Finished waiting")
	}

	svc := route53.New(sess)
	input := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String("UPSERT"),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(dns),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String(ipAddress),
							},
						},
						TTL:           aws.Int64(int64(dnsTTL)),
						Type:          aws.String("A"),
						Weight:        aws.Int64(100),
						SetIdentifier: aws.String(ipAddress),
					},
				},
			},
			Comment: aws.String("route53-sidecar"),
		},
		HostedZoneId: aws.String(hostedZone),
	}

	select {
	case sig := <-gracefulStop:
		log.Fatalf("Caught Signal before change: %+v", sig)
	default:
	}

	changeSet, err := svc.ChangeResourceRecordSets(input)
	if err != nil {
		log.Fatalf("Failed to create DNS: %v", err.Error())
	}

	log.Info("Request sent to Route 53...")
	waitForSync(changeSet)
}

func waitForSync(changeSet *route53.ChangeResourceRecordSetsOutput) {
	svc := route53.New(sess)

	for {
		time.Sleep(5 * time.Second)
		failures := 0

		changeOutput, err := svc.GetChange(&route53.GetChangeInput{
			Id: changeSet.ChangeInfo.Id,
		})

		if err != nil {
			log.Errorf("Failed getting ChangeSet result: %v", err)
			failures++
		}

		if failures > 3 {
			log.Fatal("Failed the maximum times getting changeset, exiting")
		}

		if *changeOutput.ChangeInfo.Status == "INSYNC" {
			log.Info("Route53 Change Completed")
			break
		}

		log.Infof("Route53 Change not yet propogated (ChangeInfo.Status = %s)...", *changeOutput.ChangeInfo.Status)
	}
}

type ecsMetadata struct {
	Networks []struct {
		IPv4Addresses []string `json:"IPv4Addresses"`
	} `json:"Networks"`
}

func getEcsMetadata() (*ecsMetadata, error) {
	// Get metadata URI from ECS_CONTAINER_METADATA_URI_V4 or ECS_CONTAINER_METADATA_URI
	uri := os.Getenv("ECS_CONTAINER_METADATA_URI_V4")
	if uri == "" {
		uri = os.Getenv("ECS_CONTAINER_METADATA_URI")
	}
	client := http.Client{
		Timeout: 1 * time.Second, // 1 second timeout, same as ec2metadata
	}
	resp, err := client.Get(uri)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	metadata := &ecsMetadata{}
	if err = json.NewDecoder(resp.Body).Decode(metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

func main() {
	configureFromFlags()
	dumpConfig()

	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)
	setupDNS()

	catchSignals()
}
