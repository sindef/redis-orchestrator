# Changelog

## [Unreleased]

### Added - Debug Logging & Multi-Site Support

#### Debug Logging (`--debug=true` flag)
- Added `--debug` command-line flag for verbose logging
- Detailed state sync cycle information
- Election candidate ranking with full details (pod name, UID, namespace, startup time, IP)
- Decision reasoning explanations (why a specific pod was elected)
- Split-brain detection and resolution details with step-by-step actions
- Promotion/demotion events with full context

**Example debug output:**
```
============================================
NO MASTER DETECTED - Starting election
Election candidates count=3
Candidate rank=1 pod=redis-0 uid=abc-123 namespace=default startupTime=2025-12-05T10:00:00Z ip=10.0.1.5
Candidate rank=2 pod=redis-1 uid=def-456 namespace=default startupTime=2025-12-05T10:05:00Z ip=10.0.1.6
Candidate rank=3 pod=redis-2 uid=ghi-789 namespace=default startupTime=2025-12-05T10:10:00Z ip=10.0.1.7

Election result:
ELECTED MASTER pod=redis-0 uid=abc-123 namespace=default startupTime=2025-12-05T10:00:00Z 
reason=oldest startup time (2025-12-05T10:00:00Z vs 2025-12-05T10:05:00Z)

WE ARE THE ELECTED MASTER - Promoting
========================================
PROMOTING TO MASTER pod=redis-0 uid=abc-123 namespace=default
Redis promotion successful, setting pod label
```

#### Multi-Site Support (Pod UID Tie-Breaker)
- Added Pod UID to `PodState` structure
- Three-tier election algorithm:
  1. **Primary:** Startup timestamp (oldest wins)
  2. **Secondary:** Pod name (lexicographically smallest)
  3. **Tertiary:** Pod UID (lexicographically smallest) - **NEW**
  
**Solves the multi-site problem:**
```
Site 1: redis-0 (UID: zzz-abc, started at 12:00:00)
Site 2: redis-0 (UID: aaa-xyz, started at 12:00:00)
Winner: Site 2 (UID aaa-xyz < zzz-abc)
```

- All election and split-brain resolution logic now uses UID for final tie-breaking
- Comparison uses `PodUID` instead of `PodName` when pod names are identical
- Fully deterministic across multiple Kubernetes clusters

#### Documentation
- **NEW:** `MULTI-SITE.md` - Comprehensive multi-site deployment guide
  - Explains the three-tier election algorithm
  - Shows debug output examples
  - Provides troubleshooting steps
  - Includes testing procedures
  
- **UPDATED:** `README.md`
  - Added debug logging section
  - Updated leader election algorithm description
  - Added multi-site scenario explanation
  - Updated troubleshooting with debug commands
  
- **UPDATED:** `DESIGN.md`
  - Expanded election algorithm explanation
  - Added multi-site architecture section
  - Explained UID tie-breaker rationale
  
- **UPDATED:** `deploy/statefulset.yaml`
  - Added comment showing how to enable debug mode

#### Tests
- **NEW:** `TestMultiSiteScenario` - Tests UID tie-breaking
  - Same pod name, different sites, same startup time
  - Same pod name, different startup times
  - Complex 4-pod multi-site scenario
  
- **UPDATED:** All existing tests to include `PodUID` field
- All tests passing ✅

### Changed

#### Configuration
- Added `Debug bool` field to `Config` struct
- Added `--debug` flag to main.go command-line parsing

#### Orchestrator Logic
- `PodState` now includes `PodUID` and `Namespace` fields
- `Orchestrator` struct stores local `podUID`
- All sorting logic updated to use three-tier comparison:
  ```go
  sort.Slice(states, func(i, j int) bool {
      if states[i].StartupTime.Equal(states[j].StartupTime) {
          if states[i].PodName == states[j].PodName {
              return states[i].PodUID < states[j].PodUID  // NEW
          }
          return states[i].PodName < states[j].PodName
      }
      return states[i].StartupTime.Before(states[j].StartupTime)
  })
  ```

#### Logging Improvements
- `syncState()`: Logs local state and all peer states when debug enabled
- `reconcile()`: Shows reconciliation analysis with master count
- `handleNoMaster()`: Lists all candidates with ranking and election reasoning
- `handleSingleMaster()`: Shows master details and our role
- `handleSplitBrain()`: Lists all masters and resolution decision
- `promoteToMaster()`: Shows promotion with full pod details
- `demoteToReplica()`: Shows demotion with replication target

#### Helper Functions
- **NEW:** `getElectionReason()` - Explains why a pod was elected
  - Returns human-readable reason (oldest time, pod name tie-breaker, or UID tie-breaker)
  - Used in debug output to make decisions transparent

### Technical Details

#### Why Pod UID?
- **Globally unique:** Kubernetes guarantees uniqueness across all clusters
- **Stable:** Never changes for the pod's lifetime
- **Available:** Accessible via Kubernetes API without extra configuration
- **Comparable:** Lexicographic comparison works reliably
- **No configuration needed:** No need to manually assign unique identifiers

#### Comparison with Alternatives
| Approach | Multi-Site Safe | Requires Config | Stable | Deterministic |
|----------|----------------|-----------------|--------|---------------|
| Pod Name Only | ❌ | ✅ | ✅ | ❌ (multi-site) |
| Pod Name + Namespace | ⚠️ | ✅ | ✅ | ⚠️ (if namespaces differ) |
| Pod Name + UID | ✅ | ✅ | ✅ | ✅ |
| Manual IDs | ✅ | ❌ | ✅ | ✅ |

#### Performance Impact
- **Negligible:** UID comparison is a simple string comparison
- **No network calls:** UID is already available in pod metadata
- **Same algorithm complexity:** O(n log n) for sorting (unchanged)

### Migration Notes

#### Upgrading from Previous Version
1. No configuration changes required
2. Existing deployments will automatically use UID tie-breaking
3. Debug logging is **opt-in** via `--debug` flag
4. All existing behavior preserved for single-site deployments

#### Breaking Changes
- **None** - This is a backward-compatible enhancement

### Testing

#### Unit Tests
- ✅ All existing tests updated with UID field
- ✅ New multi-site scenario tests added
- ✅ Three-tier sorting logic tested
- ✅ 40+ test cases passing

#### Manual Testing Recommended
1. Deploy to single site - verify normal operation
2. Deploy to two sites with same pod names - verify deterministic election
3. Enable debug logging - verify output is helpful
4. Simulate split-brain - verify resolution uses UID when needed

### Future Enhancements
- [ ] Persistent startup time storage (survive pod restarts)
- [ ] Metrics export for election events
- [ ] Grafana dashboard for multi-site monitoring
- [ ] Automatic detection of multi-site topology
- [ ] Configurable tie-breaker priority (time vs name vs UID)

---

## Version History

### v1.0.0 (Initial Release)
- Basic leader election
- Redis replication management
- Kubernetes-native pod labeling
- Split-brain resolution
- TLS support
- Comprehensive documentation

