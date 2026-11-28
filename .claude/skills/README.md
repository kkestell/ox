# Ox skills

The skills keep the decisions the developer has to make few and visible, keep each role
in its own context, and leave behind documents that say what the project is and does.
The step-by-step workflow is in [`AGENTS.md`](../../AGENTS.md#workflow).

## Who decides what

Every question a plan or review raises is sorted by one test: does the answer
change what the user sees or does in everyday use? Only those reach
the developer. The agents settle the rest and record why.

```mermaid
flowchart LR
    Q["A question comes up<br/>while planning or reviewing"] --> T{"Does the answer change<br/>what the user<br/>sees or does<br/>in everyday use?"}

    T -- yes --> PQ["Product question<br/>asked in the<br/>developer's terms,<br/>with a recommendation"]
    PQ --> You(("Thinking<br/>meat"))
    You --> FR[("functional-requirements.md<br/>the text the<br/>developer approved")]

    T -- no --> EQ["Engineering decision<br/>design · ownership · protocol shapes<br/>tests · how rare cases behave"]
    EQ --> Agents["Planner or reviewer<br/>settles it"]
    Agents --> Rec["Recorded under Decisions<br/>in the plan or review"]

    RC["Any change to a requirement"] -. always .-> PQ

    classDef you fill:#fde68a,stroke:#92400e,color:#000
    classDef doc fill:#dbeafe,stroke:#1e3a8a,color:#000
    class You,PQ you
    class FR doc
```

## Separate roles, separate contexts

Each role runs in its own session with fresh context. The implementer never
reviews its own work, and the reviewer never fixes what it finds, so every
change is judged by a session that did not write it. The orchestrator carries
the change between them and checks each handoff. A role may start child
sessions for parts of its own work, such as research, a disjoint part of the
implementation, or one area of a large review. The children report to the
role, which keeps every decision.

```mermaid
flowchart TB
    You(("Thinking<br/>meat")) <--> Orch["Orchestrator<br/>holds direction and scope<br/>sanity-checks each handoff, opens the PR"]

    subgraph roles [" "]
        direction LR
        Plan["Planner<br/>decides how<br/><i>writes no code</i>"]
        Work["Implementer<br/>builds and tests it<br/><i>judges nothing</i>"]
        Review["Reviewer<br/>judges it against the plan<br/>and the requirements<br/><i>fixes nothing</i>"]
        Plan -- plan --> Work
        Work -- changes --> Review
        Review -- "findings: Fix · Record · Rejected" --> Work
    end

    Orch -- "starts each role<br/>in a fresh session" --> roles
    Plan -. product questions .-> You

    Audit["Auditor<br/>finds structures that keep<br/>producing defects<br/><i>fixes nothing</i>"] -- "a finding becomes<br/>the planner's request" --> Plan

    classDef you fill:#fde68a,stroke:#92400e,color:#000
    class You you
```

## What lasts

Plans, reviews, and audits are working records: each is committed with its
change and read later as evidence. The documents in `docs/` and the
repository's issues are what last. The documents say what the project does,
the issues what is known to be wrong with it, and every role reads them before
it starts.

### Functional requirements: what the user can rely on

`docs/functional-requirements.md` belongs to the developer. It holds only
behavior the user can see, and it changes only with text the developer
approved.

```mermaid
flowchart LR
    Plan["Planner proposes<br/>exact new text"] --> You(("Thinking meat<br/>approves"))
    You --> Work["Implementer applies it<br/>word for word"]
    Work --> Review["Reviewer checks the diff<br/>word for word"]
    Review --> Orch["Orchestrator checks<br/>the requirements diff<br/>before opening the PR"]
    Orch --> FR[("functional-requirements.md")]
    FR -. "cited by title in every plan and review;<br/>a whole-repository sweep checks them all" .-> Plan

    classDef you fill:#fde68a,stroke:#92400e,color:#000
    classDef doc fill:#dbeafe,stroke:#1e3a8a,color:#000
    class You you
    class FR doc
```

### Known issues: defects that cannot be fixed yet

Every confirmed defect is fixed, unless the fix waits on something the project
cannot change yet. Issues labeled `bug` keep those in view so they are neither
lost nor rediscovered by every review.

```mermaid
flowchart LR
    Review["Reviewer confirms a defect"] --> Q{"Can it be<br/>fixed now?"}
    Q -- yes --> Fix["Fix<br/>implementer fixes it"]
    Q -- "no: needs reproducing,<br/>or waits on a dependency" --> Rec["Record"]
    Rec --> KI[("bug issue<br/>worst cost · how it was found<br/>what it waits on")]
    KI -- "skipped by later reviews<br/>until what it waits on changes" --> Review
    KI -- "evidence" --> Audit["Auditor"]
    KI -- "fixed, or shown<br/>not to exist" --> Gone["issue closed"]

    classDef doc fill:#dbeafe,stroke:#1e3a8a,color:#000
    class KI doc
```

### Working records feed the audit

```mermaid
flowchart LR
    PL["plans"] & RV["reviews"] & KI[("bug issues")] & Git["fix commits"] --> Audit["Auditor<br/>groups defects by<br/>the structure behind them"]
    Audit -- "a structure worth changing" --> Plan["Planner"]
    Audit -- "rejected, with the reason" --> Kept["not raised again until<br/>its evidence changes"]

    classDef doc fill:#dbeafe,stroke:#1e3a8a,color:#000
    classDef work fill:#f3f4f6,stroke:#6b7280,color:#000
    class KI doc
    class PL,RV,Git work
```
