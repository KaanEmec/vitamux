import SwiftUI
import VitamuxKit

/// An ECG waveform laid out at the standard paper scale, 25 mm/s and 10 mm/mV (one millimetre is
/// `pointsPerMM` points), cut into 5-second tiles. Each tile's trace is built once, decimated to
/// the lowest and highest value of each point column, so drawing never touches the 15,000 raw
/// voltages and scrolling allocates nothing. Values are drawn as recorded; nothing is marked.
struct ECGStrip {
    static let pointsPerMM: CGFloat = 4
    static let mmPerSecond: CGFloat = 25
    static let mmPerMillivolt: CGFloat = 10
    static let tileSeconds = 5.0
    static var pointsPerSecond: CGFloat { mmPerSecond * pointsPerMM }
    static var pointsPerMillivolt: CGFloat { mmPerMillivolt * pointsPerMM }

    struct Tile: Identifiable {
        /// The tile's index, so identity never changes between renders.
        let id: Int
        let width: CGFloat
        let trace: Path
    }

    /// One second of the recording for the table fallback, in millivolts.
    struct Second: Identifiable {
        let id: Int
        let low: Double
        let high: Double
    }

    let tiles: [Tile]
    let seconds: [Second]
    let duration: Double
    let count: Int
    let frequency: Double?
    /// The millivolt bounds of the paper, on whole half-millivolts.
    let low: Double
    let high: Double
    /// The paper of one tile, shared by every tile.
    let grid: (minor: Path, major: Path)
    var height: CGFloat { CGFloat(high - low) * Self.pointsPerMillivolt }

    /// Nil when the values have no time base (no sampling frequency and no offsets) or none at all.
    init?(_ document: Components.Schemas.WaveformDocument) {
        let values = document.values
        let offsets = document.offsetsS.flatMap { $0.count == values.count ? $0 : nil }
        let hz = document.samplingFrequencyHz.flatMap { $0 > 0 ? $0 : nil }
        guard !values.isEmpty, offsets != nil || hz != nil else { return nil }
        let time = { (i: Int) -> Double in offsets?[i] ?? Double(i) / hz! }
        // µV to mV, as recorded; the paper spans at least −0.5 to 1 mV and at most ±5 mV.
        let scale = document.unit == "mV" ? 1.0 : 0.001
        var lowest = Double.infinity, highest = -Double.infinity
        for v in values where v.isFinite {
            lowest = min(lowest, v * scale)
            highest = max(highest, v * scale)
        }
        guard lowest.isFinite else { return nil }
        let low = max((min(lowest, -0.5) * 2).rounded(.down) / 2 - 0.5, -5)
        let high = min((max(highest, 1) * 2).rounded(.up) / 2 + 0.5, 5)
        let duration = time(values.count - 1) + (hz.map { 1 / $0 } ?? 0)
        let y = { (mV: Double) in CGFloat(high - min(max(mV, low), high)) * Self.pointsPerMillivolt }

        var tiles: [Tile] = []
        var seconds: [Second] = []
        var path = Path()
        var tile = 0
        var column = Int.min
        // The column's smallest and largest y and the sample index each was set at, so the line
        // keeps the time order of the two extremes.
        var minY = CGFloat.infinity, maxY = -CGFloat.infinity, minAt = 0, maxAt = 0
        var second = 0, secondLow = Double.infinity, secondHigh = -Double.infinity
        var started = false

        func flushColumn() {
            guard column != Int.min else { return }
            let x = CGFloat(column)
            let (first, last) = minAt <= maxAt ? (minY, maxY) : (maxY, minY)
            if started { path.addLine(to: CGPoint(x: x, y: first)) } else { path.move(to: CGPoint(x: x, y: first)) }
            started = true
            if last != first { path.addLine(to: CGPoint(x: x, y: last)) }
            column = Int.min
            minY = .infinity
            maxY = -.infinity
        }
        func flushTile(width: CGFloat) {
            flushColumn()
            tiles.append(Tile(id: tile, width: width, trace: path))
            path = Path()
            started = false
        }
        func flushSecond() {
            if secondLow.isFinite { seconds.append(Second(id: second, low: secondLow, high: secondHigh)) }
            secondLow = .infinity
            secondHigh = -.infinity
        }

        for i in values.indices {
            let v = values[i]
            guard v.isFinite else { continue }
            let t = time(i)
            let mV = v * scale
            while Int(t) > second {
                flushSecond()
                second += 1
            }
            secondLow = min(secondLow, mV)
            secondHigh = max(secondHigh, mV)
            let index = Int(t / Self.tileSeconds)
            while index > tile {
                flushTile(width: CGFloat(Self.tileSeconds) * Self.pointsPerSecond)
                tile += 1
            }
            let x = Int(((t - Double(tile) * Self.tileSeconds) * Double(Self.pointsPerSecond)).rounded(.down))
            if x != column {
                flushColumn()
                column = x
            }
            let point = y(mV)
            if point < minY { minY = point; minAt = i }
            if point > maxY { maxY = point; maxAt = i }
        }
        flushSecond()
        let rest = duration - Double(tile) * Self.tileSeconds
        flushTile(width: max(CGFloat(rest) * Self.pointsPerSecond, 1))

        self.tiles = tiles
        self.seconds = seconds
        self.duration = duration
        self.count = values.count
        self.frequency = hz
        self.low = low
        self.high = high
        grid = Self.grid(width: CGFloat(Self.tileSeconds) * Self.pointsPerSecond, height: CGFloat(high - low) * Self.pointsPerMillivolt)
    }

    /// "30 s at 512 Hz, from −0.3 to 1.0 mV".
    var summary: String {
        let range = "from \(Format.number(seconds.map(\.low).min() ?? 0)) to \(Format.number(seconds.map(\.high).max() ?? 0)) mV"
        let rate = frequency.map { " at \(Format.number($0)) Hz" } ?? ""
        return "\(Int(duration.rounded())) s\(rate), \(range)"
    }
}

/// The strip on its paper grid, scrolled sideways. Only the visible tiles are drawn.
struct ECGStripView: View {
    let strip: ECGStrip

    var body: some View {
        ScrollView(.horizontal) {
            LazyHStack(spacing: 0) {
                ForEach(strip.tiles) { tile in
                    ECGTileView(tile: tile, grid: strip.grid, seconds: Int(ECGStrip.tileSeconds) * tile.id)
                        .frame(width: tile.width, height: strip.height)
                }
            }
        }
        .frame(height: strip.height)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("ECG waveform at 25 millimetres per second and 10 millimetres per millivolt")
        .accessibilityValue(strip.summary)
        .accessibilityIdentifier("ecgStrip")
    }
}

private struct ECGTileView: View {
    let tile: ECGStrip.Tile
    let grid: (minor: Path, major: Path)
    let seconds: Int

    var body: some View {
        // The grid takes the heart-rate hue as a paper tint only; the trace is plain ink.
        let paper = MetricHue.heartRate.color
        Canvas { context, size in
            context.stroke(grid.minor, with: .color(paper.opacity(0.18)), lineWidth: 0.5)
            context.stroke(grid.major, with: .color(paper.opacity(0.45)), lineWidth: 0.8)
            for s in 0..<Int(ECGStrip.tileSeconds) where CGFloat(s) * ECGStrip.pointsPerSecond < size.width {
                context.draw(Text("\(seconds + s) s").font(.caption2).foregroundStyle(.secondary),
                             at: CGPoint(x: CGFloat(s) * ECGStrip.pointsPerSecond + 3, y: 3), anchor: .topLeading)
            }
            context.stroke(tile.trace, with: .foreground, style: StrokeStyle(lineWidth: 1.2, lineCap: .round, lineJoin: .round))
        }
    }
}

extension ECGStrip {
    /// Paper lines every millimetre and, darker, every 5 millimetres (0.2 s and 0.5 mV).
    static func grid(width: CGFloat, height: CGFloat) -> (minor: Path, major: Path) {
        var minor = Path(), major = Path()
        let step = ECGStrip.pointsPerMM
        var i = 0
        var x: CGFloat = 0
        while x <= width {
            if i.isMultiple(of: 5) { major.addLines([CGPoint(x: x, y: 0), CGPoint(x: x, y: height)]) } else { minor.addLines([CGPoint(x: x, y: 0), CGPoint(x: x, y: height)]) }
            i += 1
            x = CGFloat(i) * step
        }
        i = 0
        var y: CGFloat = 0
        while y <= height {
            if i.isMultiple(of: 5) { major.addLines([CGPoint(x: 0, y: y), CGPoint(x: width, y: y)]) } else { minor.addLines([CGPoint(x: 0, y: y), CGPoint(x: width, y: y)]) }
            i += 1
            y = CGFloat(i) * step
        }
        return (minor, major)
    }
}
