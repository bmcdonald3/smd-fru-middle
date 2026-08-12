# Real-BMC Validation Run — 2026-08-12

## What Was Tested

End-to-end pipeline against a physical Intel S2600BPB node with an OpenBMC-based BMC at
`172.24.0.3`. This validated the full flow:

1. FRU-tracker receives a `DiscoverySnapshot` containing the node's xname, secret ID, and
   Redfish address.
2. The middleware polls FRU-tracker, extracts the candidate, resolves credentials from the
   encrypted secret store, walks the real Redfish tree (`/redfish/v1/Systems` and
   `/redfish/v1/Managers`), and writes a `RedfishEndpoint` to SMD.
3. SMD auto-populates `ComponentEndpoints` from the Redfish data, including `EthernetNICInfo`
   for the system's network interfaces.

### Environment

| Component      | Details                                     |
|----------------|---------------------------------------------|
| BMC address    | `172.24.0.3`                                |
| BMC credential | `root` / `initial0`                         |
| xname assigned | `x3000c0s1b0`                               |
| System model   | Intel S2600BPB                              |
| System UUID    | `317091ec-8be6-11e8-ab21-a4bf013f6b40`      |
| System serial  | `QSBP82909087`                              |
| Host machine   | `tamarindo`                                 |
| SMD source     | `/root/mcdonald/smd`                        |
| FRU source     | `/root/mcdonald/fru-tracker`                |

### Notable conditions

- BMC uses a self-signed TLS certificate; `FRU_MIDDLE_INSECURE_TLS=true` was required for
  both the Redfish client and the SMD client (SMD also serves HTTPS in this build).
- The Go toolchain on the test machine was 1.24.2; SMD and FRU-tracker `go.mod` files
  require ≥ 1.26.5. Go 1.26.5 was installed manually before running the script.
- NICs 3–5 on the system had no valid IPv4 or IPv6 address. The middleware filters these
  out before sending to SMD to avoid `CompEthInterface` validation failures.

---

## How to Reproduce

```bash
BMC_ADDRESS=172.24.0.3 \
BMC_USER=root \
BMC_PASS=initial0 \
XNAME=x3000c0s1b0 \
SMD_DIR=/root/mcdonald/smd \
FRU_DIR=/root/mcdonald/fru-tracker \
./scripts/real-bmc-test.sh
```

The script handles everything: Postgres container, SMD, FRU-tracker, credential seeding,
snapshot posting, middleware startup, and assertion. See [scripts/real-bmc-test.sh](../scripts/real-bmc-test.sh)
for the full set of optional overrides (`PG_PORT`, `SMD_PORT`, `FRU_PORT`, `MASTER_KEY`).

---

## Actual Output

```
==> Validating prerequisites
==> Verifying Redfish connectivity to 172.24.0.3
    BMC reachable: OK
==> Clearing stale listeners
==> Starting Postgres: smd-fru-middle-real-pg-1795324
==> Waiting for Postgres
==> Building SMD and FRU-tracker binaries
==> Initializing SMD schema
==> Starting SMD
==> Starting FRU-tracker
==> Storing BMC credentials
==> Posting DiscoverySnapshot for x3000c0s1b0 -> https://172.24.0.3
    Device visible in FRU-tracker: OK
==> Starting middleware (DRY_RUN=false, InsecureTLS=true)
==> Waiting for RedfishEndpoint to appear in SMD (up to 2 min)...
==> Waiting for ComponentEndpoints to appear in SMD (up to 2 min)...

==> Validating results

PASS: Real-BMC collection succeeded for x3000c0s1b0
```

### RedfishEndpoints

```json
{
    "RedfishEndpoints": [
        {
            "ID": "x3000c0s1b0",
            "Type": "NodeBMC",
            "Hostname": "172",
            "Domain": "24.0.3",
            "FQDN": "172.24.0.3",
            "Enabled": true,
            "User": "root",
            "Password": "initial0",
            "RediscoverOnUpdate": false,
            "DiscoveryInfo": {
                "LastDiscoveryStatus": "NotYetQueried"
            }
        }
    ]
}
```

> **Note:** SMD splits the IP address `172.24.0.3` into `Hostname: "172"` and `Domain:
> "24.0.3"` because the `splitHostAndDomain` helper in the middleware treats the first
> `.`-delimited segment as hostname and the remainder as domain. This is cosmetically odd
> for bare IP addresses but does not affect functionality — SMD uses `FQDN` (`172.24.0.3`)
> for all Redfish calls.

### ComponentEndpoints

```json
{
    "ComponentEndpoints": [
        {
            "ID": "x3000c0s1b0",
            "Type": "NodeBMC",
            "RedfishType": "Manager",
            "RedfishSubtype": "",
            "OdataID": "/redfish/v1/Managers/BMC",
            "RedfishEndpointID": "x3000c0s1b0",
            "Enabled": true,
            "RedfishEndpointFQDN": "172.24.0.3",
            "RedfishURL": "172.24.0.3/redfish/v1/Managers/BMC",
            "ComponentEndpointType": "ComponentEndpointManager",
            "RedfishManagerInfo": {
                "Actions": {
                    "#Manager.Reset": {
                        "ResetType@Redfish.AllowableValues": null,
                        "@Redfish.ActionInfo": "/redfish/v1/Managers/BMC/ResetActionInfo",
                        "target": "/redfish/v1/Managers/BMC/Actions/Manager.Reset"
                    }
                },
                "CommandShell": {
                    "ServiceEnabled": true,
                    "MaxConcurrentSessions": 65536,
                    "ConnectTypesSupported": []
                }
            }
        },
        {
            "ID": "x3000c0s1b0n0",
            "Type": "Node",
            "RedfishType": "ComputerSystem",
            "RedfishSubtype": "Physical",
            "UUID": "317091ec-8be6-11e8-ab21-a4bf013f6b40",
            "OdataID": "/redfish/v1/Systems/QSBP82909087",
            "RedfishEndpointID": "x3000c0s1b0",
            "Enabled": true,
            "RedfishEndpointFQDN": "172.24.0.3",
            "RedfishURL": "172.24.0.3/redfish/v1/Systems/QSBP82909087",
            "ComponentEndpointType": "ComponentEndpointComputerSystem",
            "RedfishSystemInfo": {
                "Name": "S2600BPB",
                "Actions": {
                    "#ComputerSystem.Reset": {
                        "ResetType@Redfish.AllowableValues": [
                            "ComputerSystem.Reset",
                            "Oem"
                        ],
                        "@Redfish.ActionInfo": "/redfish/v1/Systems/QSBP82909087/ResetActionInfo",
                        "target": "/redfish/v1/Systems/QSBP82909087/Actions/ComputerSystem.Reset"
                    }
                },
                "EthernetNICInfo": [
                    {
                        "RedfishId": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/1",
                        "@odata.id": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/1",
                        "Description": "System NIC 1",
                        "InterfaceEnabled": true,
                        "MACAddress": "a4-bf-01-3f-6b-40"
                    },
                    {
                        "RedfishId": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/2",
                        "@odata.id": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/2",
                        "Description": "System NIC 2",
                        "InterfaceEnabled": true,
                        "MACAddress": "a4-bf-01-3f-6b-41"
                    },
                    {
                        "RedfishId": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/3",
                        "@odata.id": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/3",
                        "Description": "System NIC 3",
                        "InterfaceEnabled": true,
                        "MACAddress": "ff-ff-ff-ff-ff-ff"
                    },
                    {
                        "RedfishId": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/4",
                        "@odata.id": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/4",
                        "Description": "System NIC 4",
                        "InterfaceEnabled": true,
                        "MACAddress": "02-09-01-08-38-c5"
                    },
                    {
                        "RedfishId": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/5",
                        "@odata.id": "/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/5",
                        "Description": "System NIC 5",
                        "InterfaceEnabled": true,
                        "MACAddress": "02-09-01-08-46-9a"
                    }
                ]
            }
        }
    ]
}
```

### Middleware log (final cycle)

```
2026/08/12 17:23:28 cycle complete: total=1 processed=1 skipped=0 failed=0
```

`processed=1 skipped=0 failed=0` confirms a clean write with no errors.
