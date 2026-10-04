import CryptoKit
import Foundation
import zlib

/// Splits a page into upload chunks and derives their idempotency keys. Deterministic, so a
/// re-sent page yields the same chunks and keys.
public enum Batcher {
    public static let maxSamples = 2_000
    public static let maxGzipBytes = 1 << 20

    /// Chunks of at most `maxSamples` samples whose gzip encoding (by `size`) stays within
    /// `maxGzipBytes`. Deletions ride in the last chunk. A page without content yields no chunk.
    public static func chunks(_ page: SamplesPage, size: (SamplesPage) throws -> Int) rethrows -> [SamplesPage] {
        if page.samples.isEmpty && page.deleted.isEmpty { return [] }
        var parts = stride(from: 0, to: max(page.samples.count, 1), by: maxSamples).map {
            Array(page.samples[$0..<min($0 + maxSamples, page.samples.count)])
        }
        var out: [SamplesPage] = []
        while !parts.isEmpty {
            let samples = parts.removeFirst()
            var chunk = page
            chunk.samples = samples
            chunk.deleted = parts.isEmpty ? page.deleted : []
            if samples.count > 1, try size(chunk) > maxGzipBytes {
                parts.insert(contentsOf: [Array(samples[..<(samples.count / 2)]), Array(samples[(samples.count / 2)...])], at: 0)
                continue
            }
            out.append(chunk)
        }
        return out
    }

    /// `sha256(device|type|anchor_before|first_uuid|last_uuid|count)` over samples then deletions.
    public static func idempotencyKey(deviceID: String, chunk: SamplesPage) -> String {
        let uuids = chunk.samples.map(\.uuid) + chunk.deleted.map(\.uuid)
        let input = [deviceID, chunk.type, chunk.anchor.beforeHash, uuids.first ?? "", uuids.last ?? "", String(uuids.count)]
            .joined(separator: "|")
        return hex(SHA256.hash(data: Data(input.utf8)))
    }
}

func hex<D: Sequence>(_ bytes: D) -> String where D.Element == UInt8 {
    bytes.map { String(format: "%02x", $0) }.joined()
}

/// gzip (RFC 1952) via zlib.
func gzip(_ data: Data) -> Data {
    var stream = z_stream()
    guard deflateInit2_(&stream, Z_DEFAULT_COMPRESSION, Z_DEFLATED, 15 + 16, 8, Z_DEFAULT_STRATEGY,
                        ZLIB_VERSION, Int32(MemoryLayout<z_stream>.size)) == Z_OK else { fatalError("deflateInit2 failed") }
    defer { deflateEnd(&stream) }
    var out = Data(count: Int(deflateBound(&stream, UInt(data.count))))
    let written = data.withUnsafeBytes { src in
        out.withUnsafeMutableBytes { dst in
            stream.next_in = UnsafeMutablePointer(mutating: src.bindMemory(to: Bytef.self).baseAddress)
            stream.avail_in = uInt(src.count)
            stream.next_out = dst.bindMemory(to: Bytef.self).baseAddress
            stream.avail_out = uInt(dst.count)
            precondition(deflate(&stream, Z_FINISH) == Z_STREAM_END, "deflate did not finish")
            return Int(stream.total_out)
        }
    }
    return out.prefix(written)
}
