import SwiftUI
import VitamuxKit

/// A month of days with the number of workouts on each (the panel's `Calendar`): earlier and
/// later months, and a day picked to narrow the list. Counts are a number, never a colour scale.
struct MonthCalendar: View {
    let model: WorkoutsModel

    private var calendar: Calendar {
        var calendar = Calendar.current
        calendar.timeZone = .gmt
        return calendar
    }

    var body: some View {
        VStack(spacing: 8) {
            HStack {
                Button("Earlier month", systemImage: "chevron.backward") { model.step(-1) }
                    .accessibilityIdentifier("previousMonth")
                Spacer()
                Text(title).font(.headline).accessibilityIdentifier("calendar")
                Spacer()
                Button("Later month", systemImage: "chevron.forward") { model.step(1) }
                    .disabled(model.isLatestMonth)
                    .accessibilityIdentifier("nextMonth")
            }
            .labelStyle(.iconOnly)
            .buttonStyle(.borderless)
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 4), count: 7), spacing: 4) {
                ForEach(Array(weekdays.enumerated()), id: \.offset) { _, symbol in
                    Text(symbol).font(.caption2).foregroundStyle(.secondary).accessibilityHidden(true)
                }
                ForEach(0..<leading, id: \.self) { _ in Color.clear.frame(height: 1) }
                ForEach(days, id: \.self) { date in
                    DayCell(date: date, count: model.counts[date.description] ?? 0, picked: model.picked == date.description) {
                        model.pick(date.description)
                    }
                }
            }
        }
    }

    private var title: String {
        var style = Date.FormatStyle().month(.wide).year()
        style.timeZone = .gmt
        return model.month.start(in: .gmt).formatted(style)
    }

    private var days: [LocalDate] {
        (0...(model.monthEnd.day - 1)).map { model.month.adding(days: $0) }
    }

    /// Blank cells before the 1st, from the locale's first weekday.
    private var leading: Int {
        let weekday = calendar.component(.weekday, from: model.month.start(in: .gmt))
        return (weekday - calendar.firstWeekday + 7) % 7
    }

    private var weekdays: [String] {
        let symbols = calendar.veryShortStandaloneWeekdaySymbols
        let first = calendar.firstWeekday - 1
        return Array(symbols[first...] + symbols[..<first])
    }
}

private struct DayCell: View {
    let date: LocalDate
    let count: Int
    let picked: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 2) {
                Text("\(date.day)").font(.subheadline.weight(picked ? .bold : .regular)).monospacedDigit()
                Text(count > 0 ? "\(count)" : " ")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.tint)
            }
            .frame(maxWidth: .infinity, minHeight: 40)
            .background(picked ? AnyShapeStyle(.tint.opacity(0.18)) : AnyShapeStyle(.clear), in: .rect(cornerRadius: 8))
            .overlay {
                if picked { RoundedRectangle(cornerRadius: 8).stroke(.tint, lineWidth: 1.5) }
            }
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(Format.day(date)), \(count == 1 ? "1 workout" : "\(count) workouts")")
        .accessibilityAddTraits(picked ? .isSelected : [])
        .accessibilityIdentifier("day-\(date)")
    }
}
