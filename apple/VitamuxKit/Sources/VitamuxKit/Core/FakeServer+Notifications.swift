#if DEBUG
import Foundation

// Local notifications (J22.21) on the fake. Most of their conditions are already seeded: Garmin
// needs reauthorization (FakeServer+Sources.swift), a job failed permanently two days ago and the
// last backup is ten days old (FakeServer+Dashboard.swift), and `-uitest-revoked` stalls the Apple
// Health upload (FakeServer+AppleHealth.swift). This adds the one that is not: a lab document
// waiting for review (`-uitest-lab-review`). No route of its own; the lab fixture serves it.
extension FakeServer {
    /// The document `addDocumentForReview()` adds.
    public static let reviewDocumentID = "doc_" + LabFixture.hex(0x51)

    /// Adds a document whose extraction waits for the owner's review. Synthetic, no rows.
    public func addDocumentForReview() {
        let host = profile.baseURL.host() ?? ""
        LabFixture.states.withLock { states in
            var state = states[host] ?? LabFixture.State()
            guard !state.docs.contains(where: { $0["id"] as? String == Self.reviewDocumentID }) else { return }
            state.docs.append([
                "id": Self.reviewDocumentID, "status": "needs_review", "sha256": String(repeating: "cd", count: 32),
                "filename": "synthetic-report-review.pdf", "size_bytes": LabFixture.pdf.count, "page_count": 1,
                "uploaded_at": LabFixture.at, "retention_until": NSNull(), "deleted_at": NSNull(),
            ])
            states[host] = state
        }
    }
}
#endif
