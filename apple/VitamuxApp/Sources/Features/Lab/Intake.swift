import Foundation
import OpenAPIRuntime
import PDFKit
import SwiftUI
import UIKit
import UniformTypeIdentifiers
import VisionKit
import VitamuxKit

// How a PDF gets into Vitamux from the phone: the Files picker, "Open in Vitamux" from another
// app's share sheet (`Route.labImport`), and a VisionKit document scan saved as one PDF. Every way
// is checked against the server's 20 MiB limit before it is sent (`POST /documents`, multipart).
// A scan is held in memory only and goes nowhere but the owner's server.

/// What an upload answered: the document, and whether the same PDF was already stored (200).
struct UploadOutcome: Equatable {
    var document: LabDocument
    var existing: Bool

    /// "Stored report.pdf." or "report.pdf was already stored; showing the existing document."
    func notice(name: String) -> String {
        existing ? "\(name) was already stored; showing the existing document." : "Stored \(name)."
    }
}

enum LabUpload {
    /// The server's limit (docs/architecture/lab-documents.md#storage).
    static let maxBytes = 20 * 1024 * 1024

    /// Reads a picked or shared file and sends it. The size is checked before the file is read.
    static func send(file url: URL, client: Client) async throws -> UploadOutcome {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        let values = try? url.resourceValues(forKeys: [.fileSizeKey, .contentTypeKey])
        if let type = values?.contentType, !type.conforms(to: .pdf) { throw local(.notPDF) }
        if let size = values?.fileSize, size > maxBytes { throw local(.tooLarge) }
        let data: Data
        do {
            data = try Data(contentsOf: url)
        } catch {
            throw Problem(title: "The file could not be read", detail: "Choose the PDF again, or save a copy to Files first.")
        }
        return try await send(data, filename: url.lastPathComponent, client: client)
    }

    /// Sends PDF bytes as the `file` part of a multipart form.
    static func send(_ data: Data, filename: String, client: Client) async throws -> UploadOutcome {
        guard data.count <= maxBytes else { throw local(.tooLarge) }
        guard data.starts(with: Data("%PDF-".utf8)) else { throw local(.notPDF) }
        let part = Operations.UploadDocument.Input.Body.MultipartFormPayload.file(
            .init(payload: .init(body: HTTPBody(data)), filename: filename)
        )
        do {
            switch try await client.uploadDocument(body: .multipartForm([part])) {
            case .ok(let ok): return UploadOutcome(document: try ok.body.json, existing: true)
            case .created(let created): return UploadOutcome(document: try created.body.json, existing: false)
            case .undocumented(let status, _): throw Problem(title: "Unexpected response", status: status)
            default: throw Problem(title: "Unexpected response")
            }
        } catch {
            throw refusal(Problem(error))
        }
    }

    private enum Refusal { case notPDF, tooLarge }

    private static func local(_ refusal: Refusal) -> Problem {
        switch refusal {
        case .notPDF: Problem(title: "Not added", detail: LabText.uploadReason("not_pdf"), status: 422)
        case .tooLarge: Problem(title: "Not added", detail: "The PDF is larger than 20 MiB.", status: 413)
        }
    }

    /// The server's refusal in the panel's words: the size, or the reason in `errors[0].detail`.
    private static func refusal(_ problem: Problem) -> Problem {
        var problem = problem
        if problem.status == 413 {
            problem.detail = "The PDF is larger than the server accepts (20 MiB)."
        } else if problem.status == 422, let reason = LabText.uploadReason(problem.fieldErrors.first?.detail) {
            problem.detail = reason
            problem.fieldErrors = []
        }
        return problem
    }
}

// MARK: Scan

nonisolated enum ScanPDF {
    /// One PDF of the scanned pages, JPEG-compressed so a few pages stay well under the limit.
    @concurrent static func data(from pages: [UIImage]) async -> Data? {
        let document = PDFDocument()
        for (index, image) in pages.enumerated() {
            guard let page = PDFPage(image: image, options: [.compressionQuality: 0.6]) else { return nil }
            document.insert(page, at: index)
        }
        return document.pageCount > 0 ? document.dataRepresentation() : nil
    }

    /// "Scan 5 Oct 2026 at 14.32.pdf".
    static func filename(at date: Date = .now) -> String {
        let stamp = date.formatted(date: .abbreviated, time: .shortened).replacingOccurrences(of: ":", with: ".")
        return "Scan \(stamp).pdf"
    }
}

/// VisionKit's document camera; it hands back the pages, or nil when cancelled.
struct DocumentScanner: UIViewControllerRepresentable {
    let onFinish: ([UIImage]?) -> Void

    static var isAvailable: Bool { VNDocumentCameraViewController.isSupported }

    func makeCoordinator() -> Coordinator { Coordinator(onFinish: onFinish) }

    func makeUIViewController(context: Context) -> VNDocumentCameraViewController {
        let controller = VNDocumentCameraViewController()
        controller.delegate = context.coordinator
        return controller
    }

    func updateUIViewController(_ controller: VNDocumentCameraViewController, context: Context) {}

    final class Coordinator: NSObject, VNDocumentCameraViewControllerDelegate {
        let onFinish: ([UIImage]?) -> Void

        init(onFinish: @escaping ([UIImage]?) -> Void) {
            self.onFinish = onFinish
        }

        func documentCameraViewController(_ controller: VNDocumentCameraViewController, didFinishWith scan: VNDocumentCameraScan) {
            onFinish((0..<scan.pageCount).map(scan.imageOfPage(at:)))
        }

        func documentCameraViewControllerDidCancel(_ controller: VNDocumentCameraViewController) {
            onFinish(nil)
        }

        func documentCameraViewController(_ controller: VNDocumentCameraViewController, didFailWithError error: any Error) {
            onFinish(nil)
        }
    }
}

// MARK: Intake

/// The add-document flow on the documents list: the Files picker and the scanner, each ending in
/// one upload. UI tests (`-uitest`) have no picker or camera to drive: Files picks the synthetic
/// fixture PDF and the scanner returns a synthetic page image, so everything after the system UI
/// runs as on a device (J22.23 covers the real picker and camera).
@Observable
final class LabIntake {
    var isPickingFile = false
    var isScanning = false
    private(set) var isUploading = false
    private(set) var problem: Problem?
    private(set) var outcome: (UploadOutcome, name: String)?

    func reset() {
        problem = nil
        outcome = nil
    }

    func upload(file url: URL, client: Client?) async {
        await run(name: url.lastPathComponent, client: client) { try await LabUpload.send(file: url, client: $0) }
    }

    func upload(scan pages: [UIImage], client: Client?) async {
        let name = ScanPDF.filename()
        await run(name: name, client: client) { client in
            guard let data = await ScanPDF.data(from: pages) else {
                throw Problem(title: "The scan could not be saved as a PDF", detail: "Scan the pages again.")
            }
            return try await LabUpload.send(data, filename: name, client: client)
        }
    }

    private func run(name: String, client: Client?, _ work: (Client) async throws -> UploadOutcome) async {
        guard let client else { return }
        reset()
        isUploading = true
        defer { isUploading = false }
        do {
            outcome = (try await work(client), name)
        } catch {
            problem = Problem(error)
        }
    }

    var notice: String? {
        outcome.map { $0.0.notice(name: $0.name) }
    }

    #if DEBUG
    /// The fixture a UI test "picks": the synthetic PDF, or with `-uitest-large-pdf` one over the limit.
    static func uiTestFile() -> URL {
        let large = ProcessInfo.processInfo.arguments.contains("-uitest-large-pdf")
        let url = FileManager.default.temporaryDirectory.appending(path: large ? "synthetic-large.pdf" : "synthetic-report.pdf")
        let data = large ? Data("%PDF-".utf8) + Data(count: LabUpload.maxBytes + 1) : FakeServer.syntheticLabPDF
        try? data.write(to: url)
        return url
    }

    /// A synthetic scanned page: the fixture report's lines on white.
    static func uiTestScan() -> [UIImage] {
        let size = CGSize(width: 1240, height: 1754)
        let image = UIGraphicsImageRenderer(size: size).image { context in
            UIColor.white.setFill()
            context.fill(CGRect(origin: .zero, size: size))
            let attributes: [NSAttributedString.Key: Any] = [.font: UIFont.systemFont(ofSize: 40), .foregroundColor: UIColor.black]
            for (index, line) in ["SYNTHETIC LAB - not a real report", "Glucose 5.1 mmol/L 3.9 - 5.5"].enumerated() {
                (line as NSString).draw(at: CGPoint(x: 120, y: 160 + index * 70), withAttributes: attributes)
            }
        }
        return [image]
    }
    #endif
}

/// The toolbar's add button: scan with the camera or choose from Files.
struct AddDocumentMenu: View {
    @Environment(AppState.self) private var state
    let intake: LabIntake

    var body: some View {
        Menu {
            Button("Scan with camera", systemImage: "doc.viewfinder") { scan() }
                .disabled(!DocumentScanner.isAvailable && !state.isUITest)
                .accessibilityIdentifier("scanDocument")
            Button("Choose from Files", systemImage: "folder") { pickFile() }
                .accessibilityIdentifier("chooseFile")
        } label: {
            Label("Add document", systemImage: "plus")
        }
        .disabled(intake.isUploading)
        .accessibilityIdentifier("addDocument")
    }

    private func scan() {
        #if DEBUG
        if state.isUITest {
            Task { await intake.upload(scan: LabIntake.uiTestScan(), client: state.client) }
            return
        }
        #endif
        intake.isScanning = true
    }

    private func pickFile() {
        #if DEBUG
        if state.isUITest {
            Task { await intake.upload(file: LabIntake.uiTestFile(), client: state.client) }
            return
        }
        #endif
        intake.isPickingFile = true
    }
}

extension View {
    /// The Files picker and the scanner of `intake`, presented from the screen that owns it.
    func labIntake(_ intake: LabIntake, client: Client?) -> some View {
        modifier(IntakePresenter(intake: intake, client: client))
    }
}

private struct IntakePresenter: ViewModifier {
    @Bindable var intake: LabIntake
    let client: Client?

    func body(content: Content) -> some View {
        content
            .fileImporter(isPresented: $intake.isPickingFile, allowedContentTypes: [.pdf]) { result in
                guard case .success(let url) = result else { return }
                Task { await intake.upload(file: url, client: client) }
            }
            .fullScreenCover(isPresented: $intake.isScanning) {
                DocumentScanner { pages in
                    intake.isScanning = false
                    guard let pages, !pages.isEmpty else { return }
                    Task { await intake.upload(scan: pages, client: client) }
                }
                .ignoresSafeArea()
            }
    }
}
