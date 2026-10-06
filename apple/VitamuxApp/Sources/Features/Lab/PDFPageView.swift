import PDFKit
import SwiftUI
import VitamuxKit

/// The document's pages (PDFKit, from memory) with the selected row's evidence area outlined, the
/// phone's counterpart of the panel's pdf.js viewer. `page` follows scrolling and moves the view.
struct PDFPageView: View {
    let document: PDFDocument
    @Binding var page: Int
    /// The outlined area in page fractions, origin top left; nil outlines nothing.
    let box: ExtractionRow.BboxPayload?
    /// "Row 2 (Creatinine)", for VoiceOver and UI tests.
    let rowLabel: String?

    var body: some View {
        VStack(spacing: 0) {
            PDFKitView(document: document, page: $page, box: box)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("PDF page \(page) of \(document.pageCount)")
                .accessibilityValue(box != nil ? "\(rowLabel ?? "Row") outlined" : "No row outlined")
                .accessibilityIdentifier("pdfPage")
            HStack {
                Button("Previous page", systemImage: "chevron.up") { page -= 1 }
                    .disabled(page <= 1)
                Spacer()
                Text("Page \(page) of \(document.pageCount)").font(.footnote).monospacedDigit()
                Spacer()
                Button("Next page", systemImage: "chevron.down") { page += 1 }
                    .disabled(page >= document.pageCount)
            }
            .labelStyle(.iconOnly)
            .padding(.horizontal)
            .padding(.vertical, 6)
            .background(.bar)
        }
    }
}

private struct PDFKitView: UIViewRepresentable {
    let document: PDFDocument
    @Binding var page: Int
    let box: ExtractionRow.BboxPayload?

    func makeCoordinator() -> Coordinator { Coordinator(page: $page) }

    func makeUIView(context: Context) -> PDFView {
        let view = PDFView()
        view.displayMode = .singlePageContinuous
        view.displayDirection = .vertical
        view.autoScales = true
        view.backgroundColor = .secondarySystemBackground
        view.document = document
        context.coordinator.observe(view)
        return view
    }

    static func dismantleUIView(_ view: PDFView, coordinator: Coordinator) {
        coordinator.stop()
    }

    func updateUIView(_ view: PDFView, context: Context) {
        context.coordinator.page = $page
        if view.document !== document { view.document = document }
        guard let shown = document.page(at: max(0, min(page, document.pageCount) - 1)) else { return }
        context.coordinator.outline(box, on: shown, in: view)
        if view.currentPage !== shown, context.coordinator.outlined == nil { view.go(to: shown) }
    }

    @MainActor
    final class Coordinator: NSObject {
        var page: Binding<Int>
        private(set) var outlined: PDFAnnotation?
        private var shownBox: ExtractionRow.BboxPayload?
        private weak var shownPage: PDFPage?
        private var observer: (any NSObjectProtocol)?

        init(page: Binding<Int>) {
            self.page = page
        }

        func observe(_ view: PDFView) {
            observer = NotificationCenter.default.addObserver(forName: .PDFViewPageChanged, object: view, queue: .main) { [weak self, weak view] _ in
                MainActor.assumeIsolated {
                    guard let self, let view, let current = view.currentPage, let document = view.document else { return }
                    let number = document.index(for: current) + 1
                    if self.page.wrappedValue != number { self.page.wrappedValue = number }
                }
            }
        }

        func stop() {
            if let observer { NotificationCenter.default.removeObserver(observer) }
            observer = nil
        }

        /// Replaces the outline with one around `box` on `page`, and scrolls to it.
        func outline(_ box: ExtractionRow.BboxPayload?, on page: PDFPage, in view: PDFView) {
            if box == shownBox, box == nil || page === shownPage { return }
            if let outlined { outlined.page?.removeAnnotation(outlined) }
            outlined = nil
            shownBox = box
            shownPage = page
            guard let box else { return }
            let bounds = page.bounds(for: .cropBox)
            let rect = CGRect(
                x: bounds.minX + box.x0 * bounds.width - 3,
                y: bounds.minY + (1 - box.y1) * bounds.height - 3,
                width: (box.x1 - box.x0) * bounds.width + 6,
                height: (box.y1 - box.y0) * bounds.height + 6
            )
            let annotation = PDFAnnotation(bounds: rect, forType: .square, withProperties: nil)
            let border = PDFBorder()
            border.lineWidth = 2
            annotation.border = border
            annotation.color = .systemBlue
            page.addAnnotation(annotation)
            outlined = annotation
            view.go(to: rect.insetBy(dx: 0, dy: -60), on: page)
        }
    }
}
