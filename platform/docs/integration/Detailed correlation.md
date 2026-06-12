The Correlation Engine — How It Really Works (Plain English, Full Detail)

  ---
  Start Here: What "Correlation" Actually Means

  When your infrastructure breaks, monitoring tools don't send you one alert. They send you a storm. A single Kubernetes node going offline might fire:

  - An alert from Dynatrace: "Node mps-worker-z3-08 unreachable"
  - 8 alerts from Prometheus: one per pod that crashed on that node
  - 3 alerts from Grafana: dashboards that lost their data source
  - 2 alerts from another Dynatrace problem: "Service response time degraded"
  - 1 more from PagerDuty escalating the same Dynatrace problem

  That's 15 alerts, all about the same dead node.

  Correlation is the act of recognizing that 15 different notifications are describing the same underlying event and grouping them into one incident.

  But AlertHub doesn't just group blindly. It figures out which one is the real cause and which ones are downstream effects. That distinction is everything — because you fix the cause, not the symptoms.

  ---
  The Two-Layer Architecture

  Before getting into the strategies, understand that AlertHub has two completely separate layers of correlation that run one after the other:

  LAYER 1 — DETERMINISTIC (rule-based, no guessing)
  "I know for a fact these are related"
  ↓
  LAYER 2 — PROBABILISTIC (AI + scoring, evidence-based)
  "Based on 4 different signals, I'm X% confident these are related"

  Layer 1 always runs first. If it makes a decision, Layer 2 never runs — it's not needed. Only alerts that Layer 1 can't definitively classify get passed to Layer 2.

  This is a deliberate design choice. Using AI to decide something you can already know for certain is wasteful and introduces noise. Dynatrace already knows its root cause entity — trust it. The topology graph
  already knows the node-pod relationship — trust it. Save the probabilistic engine for the genuinely ambiguous cases.

  ---
  LAYER 1: The Root Cause Engine

  File: root_cause_engine.go
  Runs: Before anything else, on every single alert

  Think of this engine as the detective who looks at the crime scene first before calling in the forensics lab. If the answer is obvious, you don't need forensics.

  It asks three questions, in priority order. If any question gives a definitive answer, it stops and acts immediately.

  ---
  Question 1: Did Dynatrace Already Tell Us the Root Cause?

  Dynatrace has its own AI (called Davis AI) built in. When it fires an alert, it often includes a field called rootCauseEntity — literally, "this is the thing that caused this problem."

  For example, a Dynatrace alert about a slow service might include:
  rootCauseEntity: HOST-abc123def456

  That's Dynatrace saying: "I don't just know there's a problem — I know it started on this specific host."

  What AlertHub does with it:

  rootCauseEntity present?
  │
  ├─ YES → Search our open incidents:
  │         "Is there already an incident involving HOST-abc123def456?"
  │         │
  │         ├─ INCIDENT EXISTS → Attach this alert to that incident
  │         │    (this alert is downstream damage, not the root)
  │         │
  │         └─ NO INCIDENT YET → This alert IS the root
  │              Create a new incident, mark this as the root cause entity
  │              Suppress all future alerts about downstream effects
  │
  └─ NO → Move to Question 2

  Why this matters: Dynatrace's Davis AI has context we don't have — it's been watching your environment for weeks and knows entity relationships. When it says "this host caused this", that's more reliable than any
   statistical model. So we trust it completely and skip all scoring.

  The suppression part: When AlertHub creates an incident from a root entity, it immediately tells Redis: "Any future alerts that are downstream of HOST-abc123def456 — mark them as suppressed, don't page
  separately." The downstream alerts still get recorded (for the blast radius list), but they don't create new incidents. This is how one server failure stays as one incident even as 30 pod alerts flood in over the
   next 2 minutes.

  ---
  Question 2: Does Our Infrastructure Map Show a Higher-Level Problem Already Exists?

  AlertHub maintains a live map of your entire infrastructure stored in Redis. It knows:

  Bare Metal Server BM-42
    └─ KVM Hypervisor KVM-05
         └─ Virtual Machine VM-prod-123
              └─ Kubernetes Node mps-worker-z3-08
                   ├─ Pod: prometheus-6d4b9f (namespace: monitoring)
                   ├─ Pod: api-gateway-8c2f1 (namespace: production)
                   └─ Pod: redis-cache-3a7e2 (namespace: production)

  This map is refreshed from your actual clusters every 5 minutes.

  When an alert arrives for Pod: api-gateway-8c2f1, AlertHub walks up the tree:

  Pod api-gateway-8c2f1
    → parent: K8s Node mps-worker-z3-08 — any open incident for this node?
         → YES: an incident about the node already exists
                → Attach pod alert to node incident, stop here
    → parent: VM vm-prod-123 — any open incident for this VM?
         → ...and so on up to bare metal

  The infra level hierarchy:

  ┌───────┬───────────────────────────┬──────────────────────────────────────────────────────────────┐
  │ Level │        Entity Type        │                            Trust                             │
  ├───────┼───────────────────────────┼──────────────────────────────────────────────────────────────┤
  │ 5     │ Bare Metal Server         │ Highest — if the BM is broken, everything below it is broken │
  ├───────┼───────────────────────────┼──────────────────────────────────────────────────────────────┤
  │ 4     │ KVM Hypervisor            │ Very high                                                    │
  ├───────┼───────────────────────────┼──────────────────────────────────────────────────────────────┤
  │ 3     │ Virtual Machine           │ High                                                         │
  ├───────┼───────────────────────────┼──────────────────────────────────────────────────────────────┤
  │ 2     │ Kubernetes Node           │ Medium-high                                                  │
  ├───────┼───────────────────────────┼──────────────────────────────────────────────────────────────┤
  │ 1     │ Kubernetes Pod / Workload │ Lower — probably a symptom                                   │
  ├───────┼───────────────────────────┼──────────────────────────────────────────────────────────────┤
  │ 0     │ Unknown                   │ Treat cautiously                                             │
  └───────┴───────────────────────────┴──────────────────────────────────────────────────────────────┘

  Higher level = more likely to be the cause, not the symptom. A bare metal server failure causes VM problems, which cause pod crashes. Never the reverse.

  What AlertHub does:

  Walk up infra tree from alert's entity
  │
  ├─ Found ancestor with open incident AND ancestor level > alert level?
  │   → Attach: this alert is downstream damage of the ancestor's problem
  │
  └─ No ancestor incident found → Move to Question 3

  ---
  Question 3: Is This Alert Itself the Root Cause of Something New?

  At this point, no existing incident matches. AlertHub asks: "Is this alert high-level enough that it's probably causing other things, rather than being caused by them?"

  Alert's infra level ≥ VM (level 3)
  AND
  Alert has blast radius data (affects multiple entities)?
  │
  ├─ YES → This alert IS a root cause
  │         Create new incident, set this alert as the root
  │         Suppress downstream alerts when they arrive
  │
  └─ NO  → Can't determine from structure alone
            Pass to Layer 2 (probabilistic engine)

  Why this threshold? A pod crash is usually a symptom. A VM crash or node crash is usually a cause. Anything at VM level or above that's taking down multiple things is definitionally a root cause — that's not a
  statistical judgment, it's an infrastructural fact.

  ---
  LAYER 2: The Parallel Correlation Engine

  File: parallel_correlation_engine.go
  Runs: Only when Layer 1 returns "I don't know"

  This is where the probabilistic, AI-powered work happens. AlertHub runs 4 completely independent strategies simultaneously, each looking at the problem from a different angle. All 4 run in parallel — the whole
  thing takes under 30 seconds total.

  Think of it like a panel of 4 expert witnesses, each with different expertise, all examining the same evidence at the same time. Their testimonies are then weighed and combined into a verdict.

  ---
  Strategy 1: The Language Expert (Semantic Correlation)

  Weight: 35% of final score
  Technology: BERT AI model + Weaviate vector database

  This strategy reads the alert's title and description and asks: "Does this mean the same thing as any recent alert, even if the words are completely different?"

  How it works:

  A BERT model (the same family of AI that powers many search engines) converts alert text into a mathematical "fingerprint" of meaning — a list of 768 numbers that represents the semantic content of the text, not
  just the words. Texts that mean similar things end up with similar number sequences.

  Example:
  - "Pod OOMKilled: container exceeded memory limit"
  - "Memory pressure on workload api-gateway: killed"
  - "Container api-gateway-8c2f1 terminated due to out-of-memory condition"

  Three completely different strings. To a word-matching system, they look different. To BERT, they're nearly identical — all three get fingerprints that are 92%+ similar.

  The flow:

  New alert arrives
  ↓
  BERT generates its meaning fingerprint (768 numbers)
  ↓
  Store fingerprint in Weaviate (the vector database) with metadata
  ↓
  Query Weaviate: "Find all alert fingerprints from the last 24 hours
                   that are at least 75% similar to this one"
  ↓
  Results: a list of alerts that semantically mean the same thing
  ↓
  Score = average cosine similarity of the closest matches

  Weaviate is essentially a database designed for this — instead of storing rows of text and searching with keywords, it stores those 768-number fingerprints and searches by mathematical similarity. It can compare
  a new fingerprint against thousands of stored ones in milliseconds.

  What happens if BERT is down? AlertHub falls back to classic text comparison: Levenshtein distance (how many characters need to change to make strings identical) + Jaccard similarity (what percentage of words do
  they share). Less accurate, but still useful.

  ---
  Strategy 2: The Time Detective (Temporal Correlation)

  Weight: 25% of final score
  Technology: Mathematical time decay formula

  This strategy ignores content entirely and asks: "Did something else happen recently, close enough in time that they're probably related?"

  The core insight: infrastructure failures are bursty. A node going down triggers 15 alerts in 3 minutes. An alert that arrives 2 minutes after 14 other alerts is almost certainly related to them.

  The math:

  AlertHub uses an exponential decay formula:

  time_score = e^(-0.1 × timeDiffMinutes / 30)

  timeDiff = 0 min   → score = 1.00 (just happened)
  timeDiff = 5 min   → score = 0.98
  timeDiff = 30 min  → score = 0.90
  timeDiff = 1 hour  → score = 0.82
  timeDiff = 2 hours → score = 0.67
  timeDiff = 6 hours → score = 0.30

  It doesn't hard-cutoff at a time boundary. It smoothly decreases confidence the older the candidate alert is. An alert from 5 minutes ago is very likely related. An alert from 5 hours ago might be related. An
  alert from yesterday is unlikely related but not impossible.

  Bonuses on top of time score:
  - Same severity (e.g., both critical): +0.10
  - Same source (e.g., both from Dynatrace): +0.05

  Why time matters: Two unrelated critical alerts from completely different parts of your infrastructure, firing 2 minutes apart, might just be coincidence. But 12 alerts firing within 90 seconds almost never is.
  The temporal strategy captures this burst pattern.

  ---
  Strategy 3: The Infrastructure Mapper (Topology Correlation)

  Weight: 25% of final score — but can override everything if score ≥ 0.60
  Technology: Redis graph, Neo4j fallback, string matching fallback

  This is the most structurally reliable strategy. It uses the live infrastructure map to ask: "Are these two alerts physically connected in our infrastructure?"

  Three tiers of lookup:

  Tier 1 — Redis Graph (Primary, fastest)

  The topology service continuously syncs your live Kubernetes clusters and VM inventory into a Redis graph. Each node in the graph knows its parent, its children, its cluster, and its labels.

  Alert comes in with entity: k8s-node-mps-worker-z3-08
  ↓
  Look up this node in Redis graph
  ↓
  Found: { cluster: mps-nonprod-rno, parent: VM-prod-123, children: [pod-1, pod-2, pod-3] }
  ↓
  Query: "Any open alerts in the last 2h involving this node or its parent?"
  ↓
  Found match on VM-prod-123: score 0.92 (same VM family)
  Found match on pod-2: score 0.92 (child of this node)

  The relationship scores:

  ┌───────────────────────────────────────┬───────┬─────────────────────────────────┐
  │             Relationship              │ Score │            Reasoning            │
  ├───────────────────────────────────────┼───────┼─────────────────────────────────┤
  │ Same bare metal server                │ 0.95  │ Almost certainly same event     │
  ├───────────────────────────────────────┼───────┼─────────────────────────────────┤
  │ VM on same bare metal                 │ 0.92  │ Shared hardware failure         │
  ├───────────────────────────────────────┼───────┼─────────────────────────────────┤
  │ Pod on same K8s node                  │ 0.92  │ Node failure causes pod failure │
  ├───────────────────────────────────────┼───────┼─────────────────────────────────┤
  │ Same K8s cluster + namespace          │ 0.85  │ Likely same deployment          │
  ├───────────────────────────────────────┼───────┼─────────────────────────────────┤
  │ Same K8s cluster, different namespace │ 0.75  │ Probably related, less certain  │
  └───────────────────────────────────────┴───────┴─────────────────────────────────┘

  These aren't guesses — they're architectural facts. A pod crashing on a node that's already alerting is, with 92% certainty, caused by that node issue.

  Tier 2 — Neo4j (fallback if Redis unavailable)

  Neo4j is a full graph database that holds a richer, more permanent picture of your enterprise infrastructure. It can run complex graph queries like: "Find all services within 3 hops of this failing host that had
  alerts in the last 2 hours." Slower than Redis, but more comprehensive.

  Tier 3 — String Matching (last resort)

  If both Redis and Neo4j are unavailable, AlertHub extracts node identifiers directly from the alert's label values and compares them by string equality against recent alerts. Much less sophisticated, but ensures
  topology awareness doesn't disappear completely during outages.

  The special rule — Topology Determinism:

  If the topology strategy returns a score ≥ 0.60, the aggregator immediately uses this as the decision, regardless of what the other 3 strategies say. A structural infrastructure match is more reliable than any
  combination of semantic and temporal signals. You don't override the org chart with statistics.

  ---
  Strategy 4: The Rule Enforcer (Rules-Based Correlation)

  Weight: 15% of final score
  Technology: Database-backed rule engine with regex, priority, and condition weighting

  This is the human knowledge layer — patterns that your team has explicitly encoded because you've seen them before.

  What a rule looks like:

  Rule: "Kubernetes OOM + Node Pressure = Same Incident"
  Priority: 180 (very high)
  Environment: production only
  Conditions:
    - alert.title CONTAINS "OOM"        (weight: 0.4, required)
    - alert.labels.cluster EQUALS $clusterName (weight: 0.3, required)
    - recent_alerts EXISTS WITH severity = "critical" (weight: 0.3)

  If all required conditions match, the rule fires and contributes a score based on its priority.

  Priority-to-score mapping:

  ┌────────────────┬────────────────────┬────────────────────┐
  │ Priority Range │ Score Contribution │      Meaning       │
  ├────────────────┼────────────────────┼────────────────────┤
  │ 200+           │ 0.98               │ Near-certain match │
  ├────────────────┼────────────────────┼────────────────────┤
  │ 150–199        │ 0.93               │ Very confident     │
  ├────────────────┼────────────────────┼────────────────────┤
  │ 100–149        │ 0.88               │ Confident          │
  ├────────────────┼────────────────────┼────────────────────┤
  │ 50–99          │ 0.75               │ Probable match     │
  └────────────────┴────────────────────┴────────────────────┘

  Condition operators available:
  - equals — exact match
  - contains — substring anywhere in the value
  - regex — full regex pattern (pre-compiled for speed)
  - gt / lt — numeric comparison (e.g., error count > 100)
  - in — value is in a list
  - exists — field is present and not null
  - starts_with / ends_with — prefix/suffix

  Rule sync: Every 5 minutes, the engine refreshes up to 1,000 rules from the database. Regex patterns are compiled once at sync time and cached — so rule evaluation during alert processing is microseconds, not
  milliseconds.

  Why only 25% weight? Rules are brittle — they only fire for patterns someone thought to write down. New failure modes the team hasn't seen before won't match any rule. The other three strategies catch what rules
  miss.

  ---
  THE AGGREGATOR: Turning 4 Scores Into One Decision

  File: correlation_aggregator.go

  After all 4 strategies return their scores, the aggregator combines them. Think of it as the judge hearing from 4 expert witnesses and making the final ruling.

  The Scoring Formula

  Raw Strategy Scores (example):
    Semantic:  0.71  ×  weight 0.25  =  0.178
    temp 0.10  =  0.213
    Topology:  0.92  ×  weight 0.35  =  0.322
    Rules:     0.00  ×  weight 0.25 rule matched)

  Composite score = 0.178 + 0.213 + 0.322 + 0.000 = 0.713

  Text overlap score = 0.45
    (how much the alert title/description overlaps with the candidate incident)

  Final score = (0.70 × 0.713) + (0.30 × 0.45)
              = 0.499 + 0.135
              = 0.634

  The Confidence Score (Stored, Not Used for Decisions)

  Confidence measures: "Do the strategies agree with each other?"

  If all strategies scored similarly (0.70, 0.75, 0.68, 0.72) → high confidence
  If scores are scattered (0.90, 0.10, 0.85, 0.05) → low confidence

  Confidence = composite score
             × strategy agreement factor
             + 0.10 if ≥2 strategies > 0.50
             + 0.05 if all 4 strategies ran successfully

  Crucially: confidence is stored in the audit trail but does not control the decision. Why? Because a single topo 0.45. Topology determinism
  always wins.

  The Decision Table

  ┌──────────────────────────────┬─────────────────┬─────────────────────────────────────────────────────────┐
  │            Signal            │    Decision     │                         Action                          │
  ├──────────────────────────────┼─────────────────┼─────────────────────────────────────────────────────────┤
  │ Topology score ≥ 0.60        │ MERGE or CREATE │ Infrastructure match is definitive — act immediately    │
  ├──────────────────────────────┼─────────────────┼─────────────────────────────────────────────────────────┤
  │ ≥ 2 strategies scored > 0.50 │ MERGE or CREATE │ Multiple independent witnesses agree — act              │
  ├──────────────────────────────┼─────────────────┼─────────────────────────────────────────────────────────┤
  │ Final score ≥ 0.40           │ MERGE or CREATE │ Moderate signal — act                                   │
  ├──────────────────────────────┼─────────────────┼─────────────────────────────────────────────────────────┤
  │ 0.20 ≤ score < 0.40          │ MONITOR         │ Weak signal — hold for 45 seconds, see if more comes in │
  ├──────────────────────────────┼─────────────────┼─────────────────────────────────────────────────────────┤
  │ Score < 0.20                 │ DISCARD         │ No meaningful signal — don't create noise               │
  └──────────────────────────────┴─────────────────┴─────────────────────────────────────────────────────────┘

  For MERGE and CREATE: if there's an existing open incident that matches, merge. If not, create a new one.

  ---
  THE DEDUPLICATION CASCADE: 11 Layers Against Duplicate Incidents

  Even after correlation makes a "create incident" decision, AlertHub runs 11 sequential checks before actually creating anything. Each check looks for an existing incident that would make a new one redundant.

  Think of these as 11 different ways two alerts can be "the same thing" — each catching a different real-world scenario.

  Check 1 — Race Condition Guard
    Two goroutines processing two related alerts simultaneously both decide to create an incident.
    A sync.Map lock prevents both from creating — the second one polls for 3 seconds to find the first.
    Catches: burst scenarios where 3 alerts arrive within milliseconds of each other.

  Check 2 — Per-Cluster Serialization
    All incident creation for the same cluster is serialized with a mutex.
    Catches: node failure + pod failures arriving within the same millisecond window.

  Check 3 — 5-Minute Title+Source Burst
    Same title prefix + same monitoring source + within 5 minutes.
    Catches: Dynatrace firing the same problem notification 3 times because of retries.

  Check 4 — 2-Hour Entity ID Match
    Same entity_id in the alert metadata (Dynatrace problem ID, for example).
    Catches: Dynatrace reopening a problem that was briefly resolved and re-fired.

  Check 5 — 6-Hour Cluster+Domain Match
    Same cluster + same problem domain (CPU, memory, disk, network, pod, node, host) within 6 hours.
    Catches: Memory pressure alert at 9am and another memory alert for same cluster at 10am —
             same underlying issue, not two separate incidents.

  Check 6 — 30-Minute Cross-Source Cascade
    Topology path or blast radius overlaps + within 30 minutes.
    Catches: Prometheus fires a node alert AND Dynatrace fires a pod alert for the same node,
             coming in seconds apart from different tools.

  Check 7 — 2-Hour Infrastructure Cascade
    Same node or host entity in topology path + within 2 hours.
    Catches: The node that crashed is restarting and firing new alerts — still the same incident.

  Check 8 — Topology Cache Lookup
    For pods: look up where this workload was *historically* running.
    Catches: A pod that got rescheduled to a new node — it looks different topologically,
             but it's the same application having the same problem.

  Check 9 — 30-Minute Fingerprint
    MD5 hash of (normalized title + severity + source) within 30 minutes.
    Catches: Identical alert fired by two different webhook retries.

  Check 10 — 2-Hour Topology Merge
    The topology strategy identified the same infrastructure node as an existing incident's topology path.
    Catches: Two alerts that both reference the same K8s node, arriving 45 minutes apart.

  Check 11 — Create New Incident
    Nothing matched. This is genuinely new. Create it.

  ---
  THE SAFETY NET: Zero Alerts Ever Lost

  After all this correlation and deduplication, AlertHub still has a final backstop.

  Every alert's current state is tracked in Redis:

  ┌──────────────────┬────────────────────────────────────────────────────┐
  │      State       │                      Meaning                       │
  ├──────────────────┼────────────────────────────────────────────────────┤
  │ BUFFERED         │ Arrived, waiting in the deferred window            │
  ├──────────────────┼────────────────────────────────────────────────────┤
  │ ATTACHED         │ Successfully merged into an existing incident      │
  ├──────────────────┼────────────────────────────────────────────────────┤
  │ INCIDENT_CREATED │ Successfully created a new incident                │
  ├──────────────────┼────────────────────────────────────────────────────┤
  │ SUPPRESSED       │ Downstream of a root cause — intentionally ignored │
  └──────────────────┴────────────────────────────────────────────────────┘

  Every 30 seconds, a background process scans Redis and asks: "Is there any alert that's been BUFFERED for more than 60 seconds without being resolved to one of the other states?"

  If yes — something went wrong somewhere. Maybe a service timed out. Maybe the deferred window expired but nobody created the incident. That alert gets reprocessed immediately.

  The math on worst-case: An alert arrives, fails to correlate, sits in BUFFERED state. The 30-second scanner finds it at most 60 seconds after it was buffered. Total worst case from alert arrival to incident
  creation: 90 seconds. No alert is ever just silently lost.

  ---
  The Audit Trail: Full Transparency

  For every single alert processed, AlertHub stores a complete record in pipeline_correlation_results:

  alert_id, alert_title, alert_source, alert_severity
  decision (what was decided)
  final_score (the combined number)
  dominant_strategy (which strategy drove the decision)
  semantic_score, temporal_score, topology_score, rules_score (individual scores)
  reasoning (human-readable explanation of why)
  ai_root_cause (LLM-generated explanation)
  matched_node_label (what infrastructure entity it matched)
  elapsed_ms (how long the whole process took)

  This means if a manager ever asks "why did these two incidents get created instead of merged?" — you can show them the exact scores, which strategies fired, and what the reasoning was. Nothing is a black box.

  ---
  The One-Paragraph Summary

  ▎ AlertHub receives alerts from every monitoring tool, normalizes them into a common format, and runs them through a two-layer engine. The first layer uses hard structural facts — Dynatrace's own root cause tags
  ▎ and a live infrastructure map — to make instant, certain decisions. Anything it can't classify goes to the second layer, where four AI strategies run simultaneously: semantic similarity (BERT AI reads meaning,
  ▎ not keywords), time proximity (burst patterns), infrastructure topology (physical relationships in the server graph), and operator-defined rules. Their scores are weighted and combined into a single decision.
  ▎ Before any new incident is created, eleven deduplication checks rule out the possibility that it's already being tracked. Every decision is stored with full audit trail. A background safety net runs every 30
  ▎ seconds to ensure zero alerts are ever silently dropped. The result: one infrastructure failure = one incident, root cause pre-identified, no noise.