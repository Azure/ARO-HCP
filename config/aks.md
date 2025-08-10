# AKS nodepool zones

## Zonal Region with enough zones for the required pools

Proposal: create zonal pools
Naming scheme: `${poolType}${zone}`, e.g. `user1`
Naming reason: backwards compatibility with existing pools

### Example with implicit zones

We will rely on the general zone information of a region provided via `.ev2.availabilityZoneCount`
and derive a zone list from it.

```yaml
userAgentPool:
    poolCount: 3
    zoneRedundantMode: "Enabled" / "Auto"
```

- `user1` will be created in zone 1
- `user2` will be created in zone 2
- `user3` will be created in zone 3

### Example with explicit zones

We can also be explicit with the zones to use if needed.

```yaml
userAgentPool:
    poolCount: 3
    zones: "1,2,3,4"
    zoneRedundantMode: "Enabled" / "Auto"
```

We want to have 3 pools and since 4 zones are available, we will create the pools as zonal ones.

- `user1` will be created in zone 1
- `user2` will be created in zone 2
- `user3` will be created in zone 3

Taking the first 3 zones from the list, is backwards compatible with existing pools.

## Non-zonal region

Proposal: create non-zonal pools
Naming scheme: `${poolType}nz${counter}`, e.g. `usernz1`
Naming reason: does not conflict with zonal pools. we don't have any clusters yet in non-zonal regions, so we can use this naming scheme without breaking anything.

### Example

The following configuration ...

```yaml
userAgentPool:
    poolCount: 3
    zones: ""
```

... will result in the following pools:

- `usernz1`
- `usernz2`
- `usernz3`

## Zonal Region with insufficient number of zones

Proposal: create as many zonal pools as possible and then fill with non-zonal pools
Assumption: Hypershift deals well with non-zonal pools
Naming scheme: same as described in the zonal and non-zonal usecases

### Example with explicit zones

```yaml
userAgentPool:
    poolCount: 3
    zones: "2"
```

We want to have 3 pools and since only one zone is available, we will create the pools as non-zonal ones.

- `usernz1`
- `user2` will be created in zone 2
- `usernz3`

upgrade strategy to 3+ zones:

- new zonal user${zone} pools will be created that don't conflict with the non-zonal pools
 a cleanup step in the pipeline can get rid of the non-zonal pools

## Helper function

AKSZoneStrategy(poolType, availableZones, requiredPools) -> array of Pool objects

type Pool = {
  name: string
  zones: array
}

Naming hints: zonal pools are named with the zone number, non-zonal pools are named with a counter.

### Examples

#### Enough Zones available

`AKSZoneStrategy('user', ['1', '2', '3'], 3) -> [Pool(name: 'user1', zones: ['1']), Pool(name: 'user2', zones: ['2']), Pool(name: 'user3', zones: ['3'])]`
`AKSZoneStrategy('user', ['1', '2', '3', '4'], 3) -> [Pool(name: 'user1', zones: ['1']), Pool(name: 'user2', zones: ['2']), Pool(name: 'user3', zones: ['3'])]`
`AKSZoneStrategy('user', ['2', '3', '4'], 3) -> [Pool(name: 'user1', zones: ['2']), Pool(name: 'user2', zones: ['3']), Pool(name: 'user3', zones: ['4'])]`

#### Not enough Zones available

`AKSZoneStrategy('user', ['1', '2'], 3) -> [Pool(name: 'user1', zones: ['1']), Pool(name: 'user2', zones: ['2']), Pool(name: 'usernz1', zones: [])]`
`AKSZoneStrategy('user', ['2'], 3) -> [Pool(name: 'user1', zones: ['2']), Pool(name: 'usernz1', zones: []), Pool(name: 'usernz2', zones: [])]`

#### No Zones available

`AKSZoneStrategy('user', [], 3) -> [Pool(name: 'usernz1', zones: []), Pool(name: 'usernz2', zones: []), Pool(name: 'usernz3', zones: [])]`
