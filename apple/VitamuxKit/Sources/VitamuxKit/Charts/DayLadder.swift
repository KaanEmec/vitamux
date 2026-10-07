import Foundation

/// One step of the Day view's ladder: a bucket size, or the stored rows.
public enum IntradayStep: String, Sendable, CaseIterable, Hashable {
    case s30 = "30s", m1 = "1m", m5 = "5m", m15 = "15m", m30 = "30m", raw

    /// The bucket length; 0 for raw rows.
    public var seconds: TimeInterval {
        switch self {
        case .s30: 30
        case .m1: 60
        case .m5: 300
        case .m15: 900
        case .m30: 1_800
        case .raw: 0
        }
    }

    /// "1-minute buckets", "30-second buckets", "raw readings".
    public var label: String {
        switch self {
        case .raw: "raw readings"
        case .s30: "30-second buckets"
        default: "\(Int(seconds / 60))-minute buckets"
        }
    }

    /// "1-minute bucket", "30-second bucket", "reading".
    public var singular: String {
        switch self {
        case .raw: "reading"
        case .s30: "30-second bucket"
        default: "\(Int(seconds / 60))-minute bucket"
        }
    }

    /// The `grain` of GET /sources/series.
    public var grain: Operations.GetSourceSeries.Input.Query.GrainPayload {
        switch self {
        case .s30: ._30s
        case .m1: ._1m
        case .m5: ._5m
        case .m15: ._15m
        case .m30: ._30m
        case .raw: .raw
        }
    }

    static let buckets: [IntradayStep] = [.s30, .m1, .m5, .m15, .m30]
}

/// A metric's Day-view ladder (docs/architecture/resolution.md#windows, J22.26), the twin of the
/// panel's `lib/explore/intraday.ts`: from the catalogue's `intraday`, each step with the widest
/// visible span it is used for. The 24-hour span reads the default bucket; zooming in walks down
/// to the finest step. A source is never drawn finer than its native spacing.
public struct DayLadder: Hashable, Sendable {
    /// One rung: the step and the widest visible span (seconds) it is used for.
    public struct Rung: Hashable, Sendable {
        public var step: IntradayStep
        public var span: TimeInterval
    }

    public let rungs: [Rung]

    public init(_ intraday: Components.Schemas.Intraday) {
        let minute: TimeInterval = 60
        let hour = 60 * minute
        let all: [Rung] = switch intraday._default {
        case ._1m: [.init(step: .m1, span: .infinity), .init(step: .s30, span: hour), .init(step: .raw, span: 15 * minute)]
        case ._5m: [.init(step: .m5, span: .infinity), .init(step: .m1, span: hour), .init(step: .raw, span: 15 * minute)]
        case ._30m: [.init(step: .m30, span: .infinity), .init(step: .m15, span: 6 * hour), .init(step: .m5, span: hour), .init(step: .m1, span: 15 * minute)]
        }
        let finest: IntradayStep = intraday.finest == .raw ? .raw : .m1
        let end = all.firstIndex { $0.step == finest } ?? all.count - 1
        rungs = Array(all[...end])
    }

    /// The step of the 24-hour span.
    public var coarsest: IntradayStep { rungs[0].step }

    /// The finest bucket (raw resolves at it).
    public var finestBucket: IntradayStep { rungs.last { $0.step != .raw }?.step ?? coarsest }

    /// The step for a visible span (seconds): the finest one whose span covers it.
    public func step(forSpan span: TimeInterval) -> IntradayStep {
        rungs.reduce(coarsest) { span <= $1.span ? $1.step : $0 }
    }

    /// The rung index of a step, nil when it is not on the ladder.
    public func index(of step: IntradayStep) -> Int? {
        rungs.firstIndex { $0.step == step }
    }

    /// The step a source is drawn at: the requested one, or the finest bucket not finer than its
    /// spacing (its rows as sent beyond 30 minutes).
    public static func sourceStep(_ requested: IntradayStep, spacing: Double?) -> IntradayStep {
        guard requested != .raw, let spacing else { return requested }
        guard let fit = IntradayStep.buckets.first(where: { $0.seconds >= spacing * 0.9 }) else { return .raw }
        return fit.seconds > requested.seconds ? fit : requested
    }

    /// The resolved bucket for a step: raw resolves at the finest bucket, never finer than the
    /// densest source the rule uses.
    public func resolvedBucket(for step: IntradayStep, spacings: [Double]) -> IntradayStep {
        let bucket = step == .raw ? finestBucket : step
        guard let densest = spacings.min() else { return bucket }
        let fit = Self.sourceStep(bucket, spacing: densest)
        return fit == .raw ? .m30 : fit
    }

    /// The span to load for a zoomed view: aligned to the step's buckets from the day's start,
    /// with a bucket of margin either side, inside the day.
    public static func loadSpan(_ step: IntradayStep, visible: ClosedRange<Date>, day: ClosedRange<Date>) -> ClosedRange<Date> {
        let length = max(step.seconds, 60)
        let start = day.lowerBound
        let lo = visible.lowerBound.timeIntervalSince(start)
        let hi = visible.upperBound.timeIntervalSince(start)
        let from = start.addingTimeInterval((lo / length).rounded(.down) * length - length)
        let to = start.addingTimeInterval((hi / length).rounded(.up) * length + length)
        return max(from, day.lowerBound) ... min(to, day.upperBound)
    }
}
