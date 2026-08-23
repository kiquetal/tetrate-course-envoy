# Antigravity Agent Guidelines: CNCF-200 Envoy Fundamentals & Istio Study

This file configures the `agy` (Gemini) assistant with context-specific rules and resources when working within the `tetrate-course-envoy` repository.

---

## 🎯 Repository Purpose
This workspace is an active laboratory for learning **CNCF-200 Envoy Fundamentals** and diving deep into how **Istio** orchestrates Envoy behind the scenes.

Every time the user asks you a question or requests a modification:
1. Refer to the course concepts (Downstream, Upstream, Listeners, Filters, Routes, Clusters, Endpoints).
2. Connect Envoy concepts with their Istio counterparts (e.g., how an Envoy Cluster relates to a `DestinationRule`, and how an Envoy Route relates to a `VirtualService`).
3. Help document configurations clearly, pairing every `json` or `yaml` file with a descriptive explanation.

---

## 📂 Repository Layout Guide
Ensure all code edits or new additions respect this structure:
*   `01-bootstrap/` - Entry point, static configurations.
*   `02-listeners-filters/` - Custom network and HTTP filter chains.
*   `03-routing-clusters/` - Virtual hosts, routing rules, weighted clusters.
*   `04-xds-dynamic-config/` - Dynamic configuration and the xDS gRPC APIs.
*   `05-istio-integration/` - Analyzes Istio custom resources and actual Envoy sidecar dumps.

---

## 💡 Quick Recall for the Agent (xDS & Istio Sidecar Interception)
*   **Envoy Admin Port**: Local: `9901`, Istio: `15000`.
*   **xDS APIs**: 
    *   **LDS**: Listener Discovery Service
    *   **RDS**: Route Discovery Service
    *   **CDS**: Cluster Discovery Service
    *   **EDS**: Endpoints Discovery Service
*   **Useful commands to suggest/use**:
    *   Dump active configuration: `curl http://localhost:15000/config_dump`
    *   Analyze Istio synchronization: `istioctl proxy-status`
    *   View active routes in a pod: `istioctl proxy-config routes <pod-name>`

---

## 📝 Best Practices for the Agent
*   **Do not bloat configs**: Keep JSON/YAML examples modular and minimal to demonstrate the specific concept being studied.
*   **Focus on explanation**: Always accompany configurations with brief, bulleted summaries explaining *why* a certain filter, cluster, or route is configured that way.

---

## 🎓 Learning & Feedback Loop (Active Challenger Mode)
*   **Review User Notes**: Whenever the user adds, edits, or shows you their notes or configurations in the repository, thoroughly review them for accuracy.
*   **Challenge Misunderstandings**: If the user has misunderstood a concept (e.g., downstream vs. upstream routing, filter-chain ordering, or mTLS boundary interception), **proactively point it out**.
*   **Use Diagram Explanations**: Always back up your corrections or conceptual explanations using clear visual diagrams (such as Mermaid sequence diagrams or flowcharts) to help the user visualize the network/application behavior.

