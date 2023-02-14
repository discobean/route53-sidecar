# route53-sidecar
Adds a route53 record on Docker startup, removes it on SIGHUP shutdown

1. Takes the IP address from EC2 or ECS metadata (or `IPADDRESS` environment)
2. Creates a weighted A record pointing to `DNS` with TTL `DNSTTL` in the `HOSTEDZONE`
3. When SIGHUP happens, it removes the created record
4. Then waits for the record to SYNC in route53 servers
5. Finally it waits for DNS TTL time to expire
6. Then exits 0

Environment variables:
* `IPADDRESS` The ip address, or set as `public-ipv4` (default) to get it from instance metadata, `ecs` to get it from ECS container metadata
* `DNS` The fully qualified DNS name to set
* `DNSTTL` The TTL time for the DNS A record entry (default 10 seconds)
* `HOSTEDZONE` The AWS Route53 Hosted Zone ID

Test from command line:
```
make build
./route53-sidecar -dns="test.example.com" -hostedzone=ABCDEFGHIJKLM4 -ipaddress=127.0.0.1
```

Use the existing docker image locally:
```
docker run -v ~/.aws:/root/.aws discobean/route53-sidecar -dns="test.example.com" -hostedzone=ABCDEFGHIJKLM4 -ipaddress=127.0.0.1
```

Build your own docker image:
```
make docker
```

Policies required for AWS ECS Role:
```
- PolicyName: route53
  PolicyDocument:
    Statement:
    - Effect: Allow
      Action:
        - route53:ChangeResourceRecordSets
      Resource: !Sub arn:aws:route53:::hostedzone/${HOSTEDZONEID}
- PolicyName: route53changes
  PolicyDocument:
    Statement:
    - Effect: Allow
      Action:
        - route53:GetChange
      Resource: "*"
```
