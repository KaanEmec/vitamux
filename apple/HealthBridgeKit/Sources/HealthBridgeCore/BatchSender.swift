import Foundation

/// Uploads one anchored page and commits its new anchor only after every chunk was accepted.
/// On failure the anchor stays put, so the next run re-reads and re-sends the same page.
public struct BatchSender: Sendable {
    let credentials: Credentials
    let anchors: AnchorStore
    let client: IngestClient
    let clientVersion: String

    public init(credentials: Credentials, anchors: AnchorStore, client: IngestClient = IngestClient(), clientVersion: String) {
        self.credentials = credentials
        self.anchors = anchors
        self.client = client
        self.clientVersion = clientVersion
    }

    public func send(_ page: SamplesPage, newAnchor: Data?) async throws {
        let fetchedAt = rfc3339(Date())
        for chunk in try Batcher.chunks(page, size: { try body(for: $0, fetchedAt: fetchedAt).count }) {
            let key = Batcher.idempotencyKey(deviceID: credentials.deviceID, chunk: chunk)
            try await client.upload(gzipBody: try body(for: chunk, fetchedAt: fetchedAt), idempotencyKey: key, credentials: credentials)
        }
        anchors.set(newAnchor, for: page.type)
    }

    func body(for chunk: SamplesPage, fetchedAt: String) throws -> Data {
        let key = Batcher.idempotencyKey(deviceID: credentials.deviceID, chunk: chunk)
        let item = IngestBatch.Item(externalKey: "\(chunk.type):\(key)", fetchedAt: fetchedAt, body: chunk)
        let batch = IngestBatch(connectionId: credentials.connectionID, client: .init(version: clientVersion), items: [item])
        return gzip(try JSONEncoder().encode(batch))
    }
}
