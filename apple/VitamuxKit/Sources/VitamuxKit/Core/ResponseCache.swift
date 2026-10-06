import CryptoKit
import Foundation
import HTTPTypes
import OpenAPIRuntime
import Synchronization

/// The offline read cache (docs/architecture/ios-app.md#offline-cache-widgets-and-notifications):
/// the client's second middleware, inside `SessionMiddleware`, so it sees the bearer token and the
/// raw status. One file per response, no database.
///
/// - Stores the `200` answers of the allow-listed `GET`s (`allowed`), keyed by server, session
///   and request path with query. Never auth, sessions, settings, keys, documents, PDFs or exports.
/// - Network first. When the server cannot be reached (or answers 5xx) it serves the stored
///   answer instead, marked with `Vitamux-Cached-At`, and `status` says since when.
/// - A pull to refresh (`refreshing`) never falls back: the owner asked for fresh data.
/// - Files live in the app group with protection `completeUntilFirstUserAuthentication`, outside
///   iCloud backup, under a fixed 100 MB cap; the least recently used go first.
/// - Emptied by a `401` to a session request and by sign-out (`logout`); the app also clears it on
///   sign-in and from Settings › This app.
public final class ResponseCache: ClientMiddleware, Sendable {
    /// Whether the last request reached the server.
    public enum Status: Equatable, Sendable {
        case online
        /// The server could not be reached; stored answers shown since then are from
        /// `showingDataFrom` or later (nil: nothing stored was shown).
        case offline(showingDataFrom: Date?)
    }

    /// The fixed cap: 100 MB.
    public static let capacity = 100 * 1024 * 1024

    /// The marker on an answer served from the cache: when it was stored (RFC 3339).
    public static let cachedAtField = HTTPField.Name("Vitamux-Cached-At")!

    /// The reads screens show offline: dashboard, catalogue, inventory, metric ranges, the
    /// specialised views, lab results and connections. Waveforms and routes stay out (large, and
    /// routes are the most sensitive data Vitamux holds).
    static let allowed: Set<String> = [
        Operations.GetDashboardLayout.id, Operations.GetResolvedSummary.id,
        Operations.ListMetrics.id, Operations.GetMetric.id, Operations.GetInventory.id,
        Operations.GetResolvedDaily.id, Operations.GetResolvedSeries.id, Operations.GetResolvedTrend.id,
        Operations.GetResolvedSources.id, Operations.GetSourceSeries.id, Operations.GetCoverage.id,
        Operations.ListMeasurements.id,
        Operations.GetResolvedSleep.id, Operations.GetResolvedWorkouts.id, Operations.ListSleep.id,
        Operations.ListBloodPressure.id, Operations.ListGroups.id, Operations.ListEvents.id,
        Operations.ListLabResults.id, Operations.GetLabResultHistory.id,
        Operations.ListConnections.id, Operations.GetConnection.id,
    ]

    /// Set by `refreshing`: requests go to the network and never fall back.
    @TaskLocal static var isRefreshing = false

    let directory: URL
    let capacity: Int
    /// The largest single answer kept: a quarter of the cap.
    var maxEntry: Int { capacity / 4 }

    private struct State {
        var status = Status.online
        var onChange: (@Sendable (Status) -> Void)?
    }

    private let state = Mutex(State())

    /// The cache in the app group's container (shared with the widget), or the app's own
    /// Application Support when the group is not available.
    public convenience init(appGroup: String) {
        let root = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
            .map { $0.appending(path: "Library/Application Support", directoryHint: .isDirectory) }
            ?? URL.applicationSupportDirectory
        self.init(directory: root.appending(path: "ResponseCache", directoryHint: .isDirectory))
    }

    /// A cache in `directory`, created on first write. UI tests and unit tests use a temporary one.
    public convenience init(directory: URL) {
        self.init(directory: directory, capacity: Self.capacity)
    }

    init(directory: URL, capacity: Int) {
        self.directory = directory
        self.capacity = capacity
    }

    // MARK: Status

    public var status: Status {
        state.withLock { $0.status }
    }

    /// Calls `handler` on every status change, from any thread.
    public func onStatusChange(_ handler: @escaping @Sendable (Status) -> Void) {
        state.withLock { $0.onChange = handler }
    }

    /// Runs `body` (a pull to refresh) with the fallback off: its requests answer from the network
    /// or fail. What they fetch is still stored.
    public static func refreshing<Value>(_ body: () async throws -> Value) async rethrows -> Value {
        try await $isRefreshing.withValue(true) { try await body() }
    }

    // MARK: Middleware

    @concurrent public func intercept(
        _ request: HTTPRequest,
        body: HTTPBody?,
        baseURL: URL,
        operationID: String,
        next: @concurrent @Sendable (HTTPRequest, HTTPBody?, URL) async throws -> (HTTPResponse, HTTPBody?)
    ) async throws -> (HTTPResponse, HTTPBody?) {
        let authorization = request.headerFields[.authorization]
        let key: String? = if request.method == .get, Self.allowed.contains(operationID), let authorization {
            Self.key(server: baseURL, authorization: authorization, path: request.path ?? "")
        } else {
            nil
        }
        let fallback = Self.isRefreshing ? nil : key
        let response: HTTPResponse
        let responseBody: HTTPBody?
        do {
            (response, responseBody) = try await next(request, body, baseURL)
        } catch {
            guard !Self.isCancellation(error) else { throw error }
            let cached = fallback.flatMap(entry(for:))
            markOffline(showingDataFrom: cached?.storedAt)
            if let cached { return served(cached) }
            throw error
        }
        if response.status.code >= 500, let cached = fallback.flatMap(entry(for:)) {
            markOffline(showingDataFrom: cached.storedAt)
            return served(cached)
        }
        if response.status.code < 500 { setStatus(.online) }
        if authorization != nil, response.status == .unauthorized || operationID == Operations.Logout.id {
            clear()
        }
        guard let key, response.status == .ok, let responseBody else { return (response, responseBody) }
        if case .known(let length) = responseBody.length, length > maxEntry { return (response, responseBody) }
        let data = try await Data(collecting: responseBody, upTo: .max)
        if data.count <= maxEntry {
            store(data, contentType: response.headerFields[.contentType], key: key)
        }
        return (response, HTTPBody(data))
    }

    private func served(_ entry: Entry) -> (HTTPResponse, HTTPBody?) {
        var fields = HTTPFields()
        if let contentType = entry.contentType { fields[.contentType] = contentType }
        fields[Self.cachedAtField] = entry.storedAt.ISO8601Format()
        return (HTTPResponse(status: .ok, headerFields: fields), HTTPBody(entry.body))
    }

    private static func isCancellation(_ error: any Error) -> Bool {
        let underlying = (error as? ClientError)?.underlyingError ?? error
        return Task.isCancelled || underlying is CancellationError || (underlying as? URLError)?.code == .cancelled
    }

    private func markOffline(showingDataFrom storedAt: Date?) {
        let previous: Date? = if case .offline(let from) = status { from } else { nil }
        setStatus(.offline(showingDataFrom: [previous, storedAt].compactMap(\.self).min()))
    }

    private func setStatus(_ status: Status) {
        let handler = state.withLock { state -> (@Sendable (Status) -> Void)? in
            guard state.status != status else { return nil }
            state.status = status
            return state.onChange
        }
        handler?(status)
    }

    // MARK: Files

    /// One stored answer.
    struct Entry: Equatable {
        var storedAt: Date
        var contentType: String?
        var body: Data
    }

    /// The first line of a file, then the body.
    private struct Header: Codable {
        var storedAt: Date
        var contentType: String?
    }

    /// A hash of server, session and request: file names reveal neither the token nor the path.
    static func key(server: URL, authorization: String, path: String) -> String {
        SHA256.hash(data: Data("\(server.absoluteString)\n\(authorization)\n\(path)".utf8))
            .map { String(format: "%02x", $0) }.joined()
    }

    private func file(_ key: String) -> URL {
        directory.appending(path: key, directoryHint: .notDirectory)
    }

    /// The stored answer for `key`; reading it makes it the most recently used.
    func entry(for key: String) -> Entry? {
        let url = file(key)
        guard let data = try? Data(contentsOf: url),
              let newline = data.firstIndex(of: UInt8(ascii: "\n")),
              let header = try? JSONDecoder().decode(Header.self, from: data[..<newline])
        else { return nil }
        try? FileManager.default.setAttributes([.modificationDate: Date.now], ofItemAtPath: url.path)
        return Entry(storedAt: header.storedAt, contentType: header.contentType, body: Data(data[(newline + 1)...]))
    }

    /// Writes one answer, then evicts down to the cap. A cache that cannot write just misses.
    func store(_ body: Data, contentType: String?, key: String, at storedAt: Date = .now) {
        do {
            try prepareDirectory()
            var data = try JSONEncoder().encode(Header(storedAt: storedAt, contentType: contentType))
            data.append(UInt8(ascii: "\n"))
            data.append(body)
            #if os(iOS)
            try data.write(to: file(key), options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            #else
            try data.write(to: file(key), options: .atomic)
            #endif
            evict()
        } catch {
            return
        }
    }

    private func prepareDirectory() throws {
        guard !FileManager.default.fileExists(atPath: directory.path) else { return }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        var url = directory
        try url.setResourceValues(values)
    }

    private static let fileKeys: [URLResourceKey] = [.fileSizeKey, .contentModificationDateKey]

    private var files: [(url: URL, size: Int, used: Date)] {
        let urls = (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: Self.fileKeys)) ?? []
        return urls.compactMap { url in
            guard let values = try? url.resourceValues(forKeys: Set(Self.fileKeys)),
                  let size = values.fileSize, let used = values.contentModificationDate
            else { return nil }
            return (url, size, used)
        }
    }

    /// Removes the least recently used files until the total fits the cap.
    private func evict() {
        let files = files
        var total = files.reduce(0) { $0 + $1.size }
        for file in files.sorted(by: { $0.used < $1.used }) where total > capacity {
            try? FileManager.default.removeItem(at: file.url)
            total -= file.size
        }
    }

    /// Bytes on disk, for Settings.
    public var size: Int {
        files.reduce(0) { $0 + $1.size }
    }

    /// Removes every stored answer.
    public func clear() {
        try? FileManager.default.removeItem(at: directory)
    }
}
