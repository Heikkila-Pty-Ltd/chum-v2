# CHUM V2: Bottom-Up Evolution of AI

## The Core Idea

Everyone is building the brain first and hoping the rest will follow. We build DNA, RNA, and proteins first — the stuff that's been robust for 3.5 billion years without a brain — and let the brain handle the small percentage of problems the lower layers can't.

Nature optimizes for efficiency. The species that die, die.

## The Problem

LLMs are predictive tokenizers. They find the most statistically probable path from their training data and converge on it. They crystallize plans and ask "approve?" — forcing humans to do mental diffs. They build engines out of cheese because cheese was the quickest path.

They have no memory of their own competence. An LLM solves your problem perfectly on Tuesday, and on Thursday it approaches the same problem from scratch — maybe it finds the same path, maybe it doesn't. Multiply stochastic coin flips across 30-50 tool calls and the variance is enormous.

Making a goldfish smarter doesn't help if it still has a 5-second memory. Building it a structure to swim through — that helps.

## The Biological Architecture Stack

This is not metaphor. It's a direct mapping of the biological stack, built bottom-up:

| Layer | Biology | System | Role |
|-------|---------|--------|------|
| DNA | Heritable instruction set | Trace recordings in graph DB | Persists across sessions, model swaps, users. Doesn't execute. |
| RNA | Transcription to executable form | Active reading of traces into runnable sequences | Bridges storage to execution |
| Proteins/Enzymes | Deterministic catalysts | Crystal candidates / deterministic scripts | Workers. Don't think. Do one job reliably. |
| Cerebellum | Prediction, error correction, motor learning | MCTS-like exploration + typed operators + verification | Predicts outcomes, checks errors, learns sequences |
| Immune system | T/B cells, antibodies, Tregs | Failure detection, negative traces, escalation watchers | Detects bad paths, prevents autoimmune (false demyelination) |
| Brain/Cortex | Novel reasoning | LLM | Most expensive, most fragile. Only for genuinely novel space. |

## The Nodes of Ranvier Model

The LLM is NOT the brain. It is the impulse through the brain — a trace of neurons through potential space. The system around it (DAG, traces, human gates) IS the brain.

- **Unmyelinated paths** = new problem space. Full fan-out exploration needed. Expensive but necessary.
- **Myelinated paths** = proven paths from successful traces. Signal jumps straight to known-good approaches (saltation). Skip exploration.
- **Nodes of Ranvier** = human decision gates. Always evaluated, never skipped. Even on proven paths, context matters.
- **Demyelination** = when a proven path fails in new context. Strip confidence, force full exploration again.

## Core Mechanisms

### Stigmergic Trace Accumulation
Intelligence lives in environmental traces, not in the agents. Each LLM invocation leaves a "pheromone trail" — tool calls, ordering, inputs, outputs, outcome. Future invocations follow strong trails, explore where none exist. Complexity emerges from accumulated trails, like termite mounds. Termites aren't smart. They follow local chemical gradients left by previous termites.

### Myelination (Progressive Crystallization)
Successful traces get weighted higher with each success. After reaching a confidence threshold (user-defined), a trace becomes a deterministic script. The LLM is no longer invoked for that path — signal "jumps" over proven segments (saltation). Thresholds are per-user, per-workflow. Joe the plumber has different tolerances than a bank.

### Demyelination (Fail-Up Response)
When a myelinated path fails, it doesn't just retry. A threshold of failures triggers a "watcher" (Treg equivalent — a larger, more capable model). The watcher investigates: environment changed? Path decayed? Transient failure? If genuinely broken: strip confidence, reopen exploration at failure point. Backtrack to nearest divergence node with pre-researched sibling approaches. Never restart from zero.

### Immune System (Antibodies + Tregs + Adversarial Probing)
Every failure is recorded as a negative trace (antibody). This prevents re-exploration of known-bad paths. Tregs = escalation to more capable model to prevent autoimmune response (demyelinating good paths due to transient failures). GAN-as-virus: adversarial probes deliberately test myelinated paths to prevent local optima stagnation — like how every flu has a new H and N, constantly testing immune systems.

### Operant Conditioning (Training Model)
The system is trained like animals, not instructed like employees. Human in loop for novel space, progressively removed as trust builds. Don't explain "why" to the system — shape behavior through reward signals on traces. Example: dogs afraid of clippers → place treats under running clippers → pick up clippers near dog → two sessions, done. Change the reward landscape, don't explain safety.

## What Gets Recorded Per Trace

1. The tool calls (name, input, output, success)
2. The ordering of tools (sequence matters — data dependencies between steps)
3. The full LLM output
4. The outcome ranking (reward backpropagation)

This is the DNA. Without it, nothing else is possible.

## Key Principles

1. **The LLM is the impulse, not the brain.** The system (DAG, traces, human gates) IS the brain.
2. **Traces are the primary artifact**, not model outputs. The path matters more than the destination.
3. **Model-agnostic by design.** Traces persist across model swaps. The aquarium stays, swap the goldfish.
4. **Time in plan space is cheap.** Explore all options before crystallizing. Counter to predictive token models that converge early.
5. **Robustness from low-level machinery**, not reasoning power. Thermophiles thrive in boiling water — not because they're smart, but because their proteins are structurally stable.
6. **User-local everything.** Confidence thresholds, myelination criteria, trust levels — all per-user. The system learns YOUR workflows, not the world's workflows.
7. **Nature optimizes for efficiency.** Minimize token burn. Deterministic where possible, stochastic only where necessary.
8. **The species that die, die.** Bad traces don't get sympathy. They get pruned. Good traces propagate. That's the entire algorithm.

## What Nobody Else Is Doing

The individual pieces exist in scattered form:
- **DSPy** (Stanford) — compiles LLM pipelines by optimizing prompts. Never crystallizes to deterministic.
- **EvoAgentX** — self-evolving agent framework. Still LLM-at-every-step.
- **TabTracer** (Feb 2026, arxiv 2602.14089v1) — MCTS for table reasoning with typed operators. 59-84% token reduction. Cross-model stable. Proves the mechanism for a narrow domain.
- **Self-Evolving Agents Survey** (arxiv 2508.07407) — best map of the space.

Nobody is doing the full stack: stigmergic trace accumulation → weighted graph of proven paths → progressive crystallization out of LLM-land into deterministic execution → immune-system failure response → user-local confidence thresholds → adversarial probing of myelinated paths.

Everyone else is trying to make models better. We're trying to make them less necessary.

## What Cortex Already Has

The substrate exists in `/projects/cortex`:
- `graph_trace_events` — tree-structured traces (parent_event_id) with rewards
- `execution_traces` + `trace_events` — tool ordering preserved across stages
- `planning_trace_events` — human decision gates tracked
- `BackpropagateReward()` — terminal reward written to all session events
- `CrystalCandidate` — serialized successful traces (OrderedSteps, Preconditions, VerificationChecks) = proteins/myelination
- Learner workflow — extracts lessons + antibodies, generates semgrep rules, crystallizes patterns
- Quality scores — provider fitness = success_rate / cost (selfish gene selection)

## V0 Scope

Record everything. Present candidates. Let the human drive.

1. **Grooming radar** — proactive tick that pre-researches top backlog candidates from beads
2. **Trace recording** — every outcome (success/failure/abandon) written back to bead nodes with reasons
3. **Presentation as OPTIONS** — "here are paths through the graph, highest confidence first, test or define further?"
4. **`chum trace`** — CLI to see full decision tree for any goal
5. **Backtracking** — on failure, go to sibling approaches (already pre-researched), don't restart

## V1 (Enabled by V0 Trace Data)

- Auto-myelination from trace success rates
- Confidence adjustment from historical data
- Deep recursive fan-out
- Adversarial probing of myelinated paths (GAN-as-virus)
- Watcher/Treg escalation on repeated failures
- Deterministic script generation from proven traces (proteins)
