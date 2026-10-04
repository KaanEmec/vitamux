import Foundation

public enum BridgeError: Error, Equatable {
    /// The token was revoked or is wrong. Pair again.
    case unauthorized
    /// The server refused the request; retrying the same request will not help.
    case rejected(status: Int)
    case badResponse
}

/// What pairing returns. Lives only in the Keychain (`TokenStore`).
public struct Credentials: Codable, Equatable, Sendable {
    public var baseURL: URL
    public var deviceID: String
    public var connectionID: String
    public var token: String

    public init(baseURL: URL, deviceID: String, connectionID: String, token: String) {
        self.baseURL = baseURL
        self.deviceID = deviceID
        self.connectionID = connectionID
        self.token = token
    }
}

/// `GET /api/ingest/v1/devices/self`: what the server knows about this device.
public struct DeviceSelf: Decodable, Equatable, Sendable {
    public struct AnchorReset: Decodable, Equatable, Sendable {
        /// A HealthKit type identifier, or `*` for every type.
        public let type: String
        public let requestedAt: Date

        public init(type: String, requestedAt: Date) {
            self.type = type
            self.requestedAt = requestedAt
        }

        enum CodingKeys: String, CodingKey { case type, requestedAt = "requested_at" }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            type = try c.decode(String.self, forKey: .type)
            let text = try c.decode(String.self, forKey: .requestedAt)
            let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
            guard let date = (try? Date(text, strategy: fractional)) ?? (try? Date(text, strategy: .iso8601)) else {
                throw DecodingError.dataCorruptedError(forKey: .requestedAt, in: c, debugDescription: "not an RFC 3339 time")
            }
            requestedAt = date
        }
    }

    public let deviceID: String
    public let connectionID: String
    public let name: String
    public let anchorResets: [AnchorReset]

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id", connectionID = "connection_id", name, anchorResets = "anchor_resets"
    }
}

public typealias Transport = @Sendable (URLRequest) async throws -> (Data, HTTPURLResponse)

/// HTTP client for the ingest API: pairing and batch upload with backoff.
public struct IngestClient: Sendable {
    public static let urlSession: Transport = { request in
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw BridgeError.badResponse }
        return (data, http)
    }

    let transport: Transport
    let sleep: @Sendable (Double) async throws -> Void
    let maxAttempts: Int

    public init(transport: @escaping Transport = urlSession, maxAttempts: Int = 6,
                sleep: @escaping @Sendable (Double) async throws -> Void = { try await Task.sleep(for: .seconds($0)) }) {
        self.transport = transport
        self.maxAttempts = maxAttempts
        self.sleep = sleep
    }

    /// Exchanges a pairing code for a device token at `POST /api/ingest/v1/devices/pair`.
    public func pair(baseURL: URL, code: String, deviceName: String) async throws -> Credentials {
        var request = URLRequest(url: baseURL.appending(path: "api/ingest/v1/devices/pair"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(["code": code, "name": deviceName])
        let (data, response) = try await send(request)
        guard (200..<300).contains(response.statusCode) else { throw Self.error(response.statusCode) }
        struct Paired: Decodable { let device_id, connection_id, token: String }
        let p = try JSONDecoder().decode(Paired.self, from: data)
        return Credentials(baseURL: baseURL, deviceID: p.device_id, connectionID: p.connection_id, token: p.token)
    }

    /// Reads this device's record, including the anchor resets the owner requested on the server.
    public func deviceSelf(credentials: Credentials) async throws -> DeviceSelf {
        var request = URLRequest(url: credentials.baseURL.appending(path: "api/ingest/v1/devices/self"))
        request.setValue("Bearer \(credentials.token)", forHTTPHeaderField: "Authorization")
        let (data, response) = try await send(request)
        guard (200..<300).contains(response.statusCode) else { throw Self.error(response.statusCode) }
        return try JSONDecoder().decode(DeviceSelf.self, from: data)
    }

    /// Posts the client checkpoint to `POST /api/ingest/v1/heartbeat` (schemas/heartbeat.v1.json): per-type anchor
    /// hashes for `healthkit.samples.v1`, never raw anchors or health values. The server answers `204`.
    /// `errorClass` is lowercase snake case, e.g. `network`, `reauth_required`, `permanent`, `transient`.
    public func heartbeat(credentials: Credentials, clientVersion: String, anchorHashes: [String: String], lastSuccessAt: Date?,
                          failedUnits: Int, errorClass: String?, now: Date = Date()) async throws {
        struct Stream: Encodable {
            let stream = "healthkit.samples.v1"
            let checkpoint: [String: [String: String]]
            let lastSuccessAt: String?
            let pendingFailedUnits: Int
            let lastErrorClass: String?
            enum CodingKeys: String, CodingKey {
                case stream, checkpoint, lastSuccessAt = "last_success_at", pendingFailedUnits = "pending_failed_units"
                case lastErrorClass = "last_error_class"
            }
        }
        struct Body: Encodable {
            let schema = "vitamux.ingest.heartbeat/1"
            let connectionID: String
            let client: [String: String]
            let sentAt: String
            let pendingFailedUnits: Int
            let lastErrorClass: String?
            let lastErrorAt: String?
            let streams: [Stream]
            enum CodingKeys: String, CodingKey {
                case schema, client, streams, connectionID = "connection_id", sentAt = "sent_at"
                case pendingFailedUnits = "pending_failed_units", lastErrorClass = "last_error_class", lastErrorAt = "last_error_at"
            }
        }
        let stream = Stream(checkpoint: ["anchors": anchorHashes], lastSuccessAt: lastSuccessAt.map { rfc3339($0) },
                            pendingFailedUnits: failedUnits, lastErrorClass: errorClass)
        let body = Body(connectionID: credentials.connectionID,
                        client: ["kind": "device", "name": "healthbridge-ios", "version": clientVersion],
                        sentAt: rfc3339(now), pendingFailedUnits: failedUnits, lastErrorClass: errorClass,
                        lastErrorAt: errorClass == nil ? nil : rfc3339(now), streams: [stream])
        var request = URLRequest(url: credentials.baseURL.appending(path: "api/ingest/v1/heartbeat"))
        request.httpMethod = "POST"
        request.httpBody = try JSONEncoder().encode(body)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(credentials.token)", forHTTPHeaderField: "Authorization")
        let (_, response) = try await send(request)
        guard (200..<300).contains(response.statusCode) else { throw Self.error(response.statusCode) }
    }

    /// Posts a gzip batch. Returns once the server accepted it (`2xx`), or already holds a batch
    /// under the same key (`409`: the key covers the same samples, sent earlier with another `fetched_at`).
    public func upload(gzipBody: Data, idempotencyKey: String, credentials: Credentials) async throws {
        var request = URLRequest(url: credentials.baseURL.appending(path: "api/ingest/v1/batches"))
        request.httpMethod = "POST"
        request.httpBody = gzipBody
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("gzip", forHTTPHeaderField: "Content-Encoding")
        request.setValue(idempotencyKey, forHTTPHeaderField: "Idempotency-Key")
        request.setValue("Bearer \(credentials.token)", forHTTPHeaderField: "Authorization")
        let (_, response) = try await send(request)
        guard (200..<300).contains(response.statusCode) || response.statusCode == 409 else {
            throw Self.error(response.statusCode)
        }
    }

    /// Sends with exponential backoff and full jitter on network errors, 408, 429 and 5xx;
    /// a `Retry-After` in seconds wins over the computed delay.
    func send(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        var attempt = 0
        while true {
            attempt += 1
            var retryAfter: Double?
            do {
                let (data, response) = try await transport(request)
                let s = response.statusCode
                guard s == 408 || s == 429 || s >= 500, attempt < maxAttempts else { return (data, response) }
                retryAfter = response.value(forHTTPHeaderField: "Retry-After").flatMap(Double.init)
            } catch is URLError where attempt < maxAttempts {}
            try await sleep(retryAfter ?? Double.random(in: 0...min(300, pow(2, Double(attempt)))))
        }
    }

    static func error(_ status: Int) -> BridgeError {
        status == 401 || status == 403 ? .unauthorized : .rejected(status: status)
    }
}
