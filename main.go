package main

import (
	"os"
	"os/signal"
	"sync"
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
	dns          string
	hostedZone   string
	healthChecks string
	dnsTTL       int
	ipAddress    string

	gracefulStop = make(chan os.Signal)
	sess         = session.Must(session.NewSession())

	wg sync.WaitGroup
)

func configureFromFlags() {
	flag.StringVar(&dns, "dns", "my.example.com", "DNS name to register in Route53")
	flag.StringVar(&hostedZone, "hostedzone", "Z2AAAABCDEFGT4", "Hosted zone ID in route53")
	flag.IntVar(&dnsTTL, "dnsttl", 10, "Timeout for DNS entry")
	flag.StringVar(&ipAddress, "ipaddress", "public-ipv4", "IP Address for A Record")
	flag.Parse()

	if ipAddress == "public-ipv4" {
		log.Infof("Fetching IP Address from EC2 public-ipv4")
		metadata := ec2metadata.New(sess)
		publicIpv4, err := metadata.GetMetadata("public-ipv4")
		if err != nil {
			log.Fatalf("Failed to fetch IPV4 public IP: %v", err)
		}
		ipAddress = publicIpv4
	}
}

func dumpConfig() {
	log.Infof("DNS=%v\n", dns)
	log.Infof("DNSTTL=%v\n", dnsTTL)
	log.Infof("HOSTEDZONE=%v\n", hostedZone)
	log.Infof("IPADDRESS=%v\n", ipAddress)
}

func catchSignals() {
	defer wg.Done()
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

	changeSet, err := svc.ChangeResourceRecordSets(input)
	if err != nil {
		log.Fatalf("Failed to create DNS: %v", err.Error())
	}

	log.Info("Request sent to Route 53...")
	waitForSync(changeSet)
}

func waitForSync(changeSet *route53.ChangeResourceRecordSetsOutput) {
	svc := route53.New(sess)

	for true {
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

func main() {
	configureFromFlags()
	dumpConfig()

	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)

	wg.Add(1)
	go catchSignals()
	setupDNS()

	wg.Wait()
}
