# route53-sidecar
Adds a route53 record on Docker startup, removes it on SIGHUP shutdown

1. Takes the public IP address from ec2 metadata (or `IPADDRESS` environment)
2. Creates a weighted A record pointing to `DNS` with TTL `DNSTTL` in the `HOSTEDZONE`
3. When SIGHUP happens, it removes the created record
4. Then waits for the record to SYNC in route53 servers
5. Finally it waits for DNS TTL time to expire
6. Then exits 0

Environment variables:
* `IPADDRESS` The ip address, or set as `public-ipv4` (default) to get it from instance metadata
* `DNS` The fully qualified DNS name to set
* `DNSTTL` The TTL time for the DNS A record entry
* `HOSTEDZONE` The AWS Route53 Hosted Zone ID

Test from command line:
```
make build
./route53-sidecar -dns="test.example.com" -hostedzone=ABCDEFGHIJKLM4 -ipaddress=127.0.0.1
```

Build the docker image:
```
make docker
```
