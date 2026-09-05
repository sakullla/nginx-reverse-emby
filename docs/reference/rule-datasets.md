# Rule datasets

`go-agent/pkg/datasets` is the shared parser and immutable local index used by
Host and Agent. Plugins consume the public SDK query/catalog contract. The
package does not fetch arbitrary URLs, issue plugin grants, or decide whether a
match means allow, deny, direct, or upstream.

## API and immutable artifacts

The public entry points are:

```go
Compile(ctx context.Context, input Input, limits Limits) (*Index, error)
LoadIndex(ctx context.Context, encoded []byte, limits Limits) (*Index, error)
CommunityFiles(data []byte, limits Limits) (map[string][]byte, error)
FilesDigest(files map[string][]byte, limits Limits) (string, error)

index.Version() pluginsdk.DatasetVersion
index.Stats() Stats
index.MarshalBinary() ([]byte, error)
index.Query(ctx, pluginsdk.DatasetQueryRequest) (pluginsdk.DatasetQueryResponse, error)
index.Catalog(pluginsdk.DatasetCatalogRequest) (pluginsdk.DatasetCatalogResponse, error)
Provinces() []Province
```

`Input` requires a validated SDK source, fixed revision, RFC3339 fetch timestamp,
and expected SHA-256. Supply either the complete `Data` artifact or a complete
community `Files` collection. File-collection digests cover sorted names and
length-delimited contents; use `FilesDigest` to produce them. Artifact digests
cover original bytes, including compression. A mismatch rejects the candidate.
All parsing, dependency resolution, indexing and validation must succeed before
`Compile` returns an index. The returned index is safe for concurrent reads and
does not retain raw input blobs. Its public results and encoded bytes are copies.

The serialized index is deterministic gzip-compressed JSON with schema
`nre.dataset-index/v1`, immutable provenance, and ordered classifications.
`Version.IndexDigest` hashes the complete `MarshalBinary` result.
`Version.Digest` hashes the independently constructed version manifest with its
digest field empty. The artifact contains neither digest as a self-reference.
`LoadIndex` rebuilds the index and requires exact canonical bytes, producing the
same version metadata. Host must compare both the downloaded artifact digest
and the reconstructed version against its desired revision.

`Version.IndexBytes` is the serialized artifact size. Use
`Stats.EstimatedMemoryBytes` for the conservative retained-index estimate when
accounting for simultaneously referenced snapshots; it is not process RSS.
Host owns aggregate node budgets, publication, generation binding, grants,
revocation, retention and desired/applied/last-good acknowledgements. A query
checks its source/version but cannot authenticate an opaque handle by syntax.
Host must resolve that handle against the authenticated caller's live grant.

## Formats and matching

| SDK format | Complete input | Semantics |
| --- | --- | --- |
| `geoip` | V2Ray `GeoIPList` protobuf, optionally gzip | IPv4/IPv6 CIDRs and `inverse_match` |
| `geosite` | V2Ray `GeoSiteList` protobuf, optionally gzip | Exact domain, root-domain boundary, literal keyword, Go regular expression, typed boolean/integer attributes |
| `community` | Full repository tar, tar.gz, zip, or `Files` | Includes, positive/negative attribute filtering, affiliation targets, cycle/missing dependency rejection |
| `cidr` | Native JSON, optionally gzip | Independent country, region and ordinary CIDR classifications |
| `geo-mmdb` | MMDB, optionally gzip | CN country and 31 mainland province projections, retaining observed coverage only |

Binary field meanings follow the upstream
[V2Ray routercommon schema](https://github.com/v2fly/v2ray-core/blob/master/app/router/routercommon/common.proto).
Stored classification names are lower-case; lookup is case-insensitive. A
GeoIP/CIDR family with no prefixes is an empty set. Inverse matching takes its
true complement, including IPv6 for an IPv4-only set; the complement of an empty
set is an explicit match-all predicate. This is separate from geographic
province coverage.

Community imports follow the upstream
[DLC source syntax](https://github.com/v2fly/domain-list-community/blob/cb663f66025ef3be1c1c7eb367dfac5f46645ffc/README.md#structure-of-data).
For example, `include:vendor @cn @-ads` filters included records rather than
adding attributes to them. Affiliations are resolved into their target lists
before includes, and semantic records are deduplicated afterward. The literal
attribute `!cn` remains distinct from negating the `cn` predicate. SDK v0.8.1
contains the corresponding attribute-name contract fix.

Archive handling stays in the shared package. It accepts normal Git global PAX
comments, rejects traversal, links, duplicate file names and unsafe overrides,
and limits compressed/expanded bytes and member counts. A lone file with an
unavailable include dependency cannot become a valid candidate. Unsupported
regex syntax or excessive regex programs fail the candidate; records are never
silently discarded to fit a budget.

A native region input can look like this (documentation addresses only):

```json
{
  "schema": "nre.cidr-dataset/v1",
  "classifications": [
    {"name": "cn-44", "kind": "region", "display_name": "广东省", "cidrs": ["192.0.2.0/24"]},
    {"name": "cn-32", "kind": "region", "display_name": "江苏省", "cidrs": ["198.51.100.0/24"]}
  ]
}
```

Native prefixes must be canonical masked CIDRs. Overlapping different province
classifications are rejected. Adding a separate `country:cn` classification
does not extend any region's match or coverage. For a province whitelist, only
`covered` and `matched=true` satisfies the selected province condition; other
provinces, unknown ranges and unsupported families do not. A province blacklist
must preserve unknown status for the plugin's explicit remaining/default rules.

## Province source and attribution

The verified province source is **DB-IP City Lite, September 2026**. Its
[official download page](https://db-ip.com/db/download/ip-to-city-lite) identifies
the data as CC BY 4.0 and permits application use with attribution. Pages using
or displaying its results must link to DB-IP. Configure:

```json
{
  "format": "geo-mmdb",
  "url": "https://download.db-ip.com/free/dbip-city-lite-2026-09.mmdb.gz",
  "license_url": "https://creativecommons.org/licenses/by/4.0/",
  "attribution_text": "IP Geolocation by DB-IP",
  "attribution_url": "https://db-ip.com"
}
```

Render attribution as escaped text and a separate HTTP(S) link. The compiler
copies license/attribution fields into immutable version provenance so a later
source edit cannot change an older snapshot's credit. License metadata records
the selected source's terms; a digest alone does not establish licensing rights.

The [official MMDB schema](https://db-ip.com/db/format/ip-to-city-lite/mmdb.html)
contains country and subdivision fields. The importer validates referenced
records through bounded cursor traversal, checks cancellation, and retains only
CN country and recognized province prefixes. It uses the pinned ISC-licensed
`github.com/oschwald/maxminddb-golang/v2 v2.5.0` reader with global string caching
disabled. It does not allocate global city record maps or retain the MMDB.

`Provinces()` provides 31 six-digit administrative codes, Chinese display names
and classification keys (`440000` / `广东省` / `cn-44`, for example). The actual
fixed file contains IPv4 and IPv6 records for all 31 options, but each remains
**partial** coverage. CN records lacking a recognized province and addresses
outside the known province ranges return `unknown`; a region source with no
records for an address family returns `unsupported-family`. Country-level IPv6
records never substitute for missing province IPv6 coverage. DB-IP Lite's
accuracy and coverage are limited; these results are dataset classifications,
not proof of a person's physical location.

No raw databases are bundled or committed. Community data uses the upstream
[MIT license](https://github.com/v2fly/domain-list-community/blob/cb663f66025ef3be1c1c7eb367dfac5f46645ffc/LICENSE).
The GeoIP project's [license section](https://github.com/v2fly/geoip#license)
identifies CC BY-SA 4.0 for the project and CC BY 4.0 for its DB-IP data.
Publishers must retain the applicable source notices and attribution when
redistributing derived artifacts. There is no ForwardX format or runtime
compatibility and no reliance on unverified province-data redistribution rights.

The explicitly requested **Loyalsoldier/v2ray-rules-dat** provider is also
verified using both original assets from
[release 202609042338](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202609042338).
Their SHA-256 values were checked against the release API's asset digests,
the release's companion `.sha256sum` files, and independently hashed downloaded
bytes. These checks establish fixed input identity, not an independent signature.
The two cases preserve their own provider IDs, original asset URLs, tagged
[repository license](https://github.com/Loyalsoldier/v2ray-rules-dat/blob/202609042338/LICENSE)
(GPL v3), and contributor attribution in the version and reloaded index.
The [tagged provider README](https://github.com/Loyalsoldier/v2ray-rules-dat/blob/202609042338/README.md#规则文件生成方式)
describes additional upstream data sources; their applicable terms remain part
of redistribution provenance. The integration test actually reads Loyalsoldier
bytes, verifies CN GeoIP matching and `category-ai-!cn` matching for
`api.openai.com`, and does not substitute the separate V2Fly fixtures.

## Bounds and verification

Use `DefaultLimits()` and reduce individual values for tighter Host grants;
an entirely zero `Limits{}` selects those defaults. Defaults are 128 MiB input,
256 MiB expansion, 2,000,000 retained semantic entries, 10,000 classifications,
64 include levels, 512 MiB serialized/estimated retained index limits, and
120 seconds per import. MMDB traversal additionally caps scanned records at
20,000,000 and individual record complexity. JSON is checked for duplicate keys,
nesting, token size and allocation estimates before typed decoding. Query limits
are the SDK's 64 requested classifications and explicit time/response budgets;
cancelled or over-budget queries return a failure, not an ordinary non-match.

The fixed integration inputs are recorded in `sources_integration_test.go`:

| Source | Fixed revision | Original SHA-256 |
| --- | --- | --- |
| V2Fly `geoip.dat` | `202609040609` | `1cba1f0982cf62502fa079c66047c3d0c608196da5b3305671e68f60e917a482` |
| V2Fly `dlc.dat` | `20260904020013` | `f82f26c015f9726c763d96a5f658e5b31b285dc094a985e718051e421f350ed6` |
| DLC source tar.gz | `cb663f66025ef3be1c1c7eb367dfac5f46645ffc` | `ce78b02633037eb64b034564bb7c8244e90d9b1335298b7b91057ff9f8d5ab25` |
| DLC source zip | same commit | `842ab69a418901bfa34c32c465bdf5cb8af935a474fcbc6854dc0325ab381a30` |
| Loyalsoldier `geoip.dat` | `202609042338` | `4149e607530f91da697bad4696f8c59f0a475af38e69405e4124438c9886c721` |
| Loyalsoldier `geosite.dat` | `202609042338` | `bca29c80611ee4b909ecc0bd531cf05901b1502998d88bf01580152ffc9e260b` |
| DB-IP City Lite MMDB gzip | `2026-09` | `c5d05b35a45c3eea0cadc728c8f5ad751693d4e270529b731442172a73f05954` |

The decompressed DB-IP MMDB is 127,339,927 bytes with SHA-256
`05a10861259c7966cb54d7181ef8c360de8c8829d182098c0e62a9b7d54cd50d`.
These are integrity pins, not independent publisher signatures.

A complete local verification on 2026-09-05 produced:

| Input | Classes / entries | Index bytes | Estimated retained bytes |
| --- | ---: | ---: | ---: |
| Full GeoIP | 253 / 1,381,586 | 5,603,029 | 205,186,458 |
| Full GeoSite | 1,539 / 109,381 | 698,402 | 28,948,135 |
| Complete DLC tar source | 1,539 / 114,296 | 728,618 | 30,161,797 |
| Complete DLC zip source | 1,539 / 114,296 | 728,778 | 30,161,957 |
| Loyalsoldier GeoIP | 260 / 1,084,116 | 4,793,131 | 162,754,178 |
| Loyalsoldier GeoSite | 1,546 / 518,210 | 3,569,511 | 129,404,468 |
| CN projection from full DB-IP | 32 / 315,077 | 1,325,299 | 47,140,607 |

The DB-IP scan covered 14,345,887 network records. Its 31 province queries and
unknown IPv6 assertion passed; observed retained Go heap growth was about
39.7 MB. Country and province prefix references are counted separately in the
CN entry total. Source-derived and precompiled DLC entry counts can differ due
to upstream redundant-domain pruning; includes and attribute semantics are
preserved. Timing/heap values are local observations, not runtime guarantees.

Run:

```sh
go -C go-agent test -count=1 -timeout=120s ./pkg/datasets/...
go -C go-agent test -tags=integration -count=1 -timeout=180s ./pkg/datasets/... -run '^TestIntegrationDatasetSources$' -v
```

The integration suite downloads these fixed sources unless
`NRE_DATASET_SOURCE_CACHE` names a directory containing the exact seven files.
The Loyalsoldier cache filenames are `loyalsoldier-geoip.dat` and
`loyalsoldier-geosite.dat`, keeping them distinct from V2Fly's `geoip.dat` and
`dlc.dat`. The remaining names are `dlc.tar.gz`, `dlc.zip`, and
`dbip-city-lite-2026-09.mmdb.gz`. Cached files undergo the same digest/size checks. Missing files, network
failure, digest mismatch or incomplete categories fail explicitly; none silently
skip. The suite verifies catalog/query behavior and canonical artifact reloads,
including the real `category-ai-!cn` match. Ordinary unit tests remain offline.
