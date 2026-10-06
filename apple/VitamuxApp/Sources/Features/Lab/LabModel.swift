import Foundation
import Observation
import VitamuxKit

/// The documents list (the panel's `/lab`): every stored PDF (`GET /documents`, every page),
/// newest first, with its status and the action it allows, and deletion keeping or deleting the
/// results confirmed from it (`DELETE /documents/{id}?derived=`).
@Observable
final class LabModel {
    enum Derived: String {
        case keep, delete
    }

    private(set) var documents: Loadable<[LabDocument]> = .loading
    private(set) var notice: String?
    private(set) var problem: Problem?

    func load(_ client: Client?) async {
        guard let client else { return }
        documents = await Loadable {
            try await readAll { cursor in
                let page = try await client.listDocuments(query: .init(limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.documents, nextCursor(page.value1))
            }
            .sorted { $0.uploadedAt > $1.uploadedAt }
        }
    }

    func show(_ notice: String?) {
        self.notice = notice
        problem = nil
    }

    func delete(_ document: LabDocument, derived: Derived, client: Client?) async {
        guard let client else { return }
        notice = nil
        problem = nil
        do {
            _ = try await client.deleteDocument(path: .init(id: document.id), query: .init(derived: derived == .keep ? .keep : .delete)).noContent
            notice = derived == .keep
                ? "Deleted \(document.name); its confirmed results are kept."
                : "Deleted \(document.name) and its results."
            await load(client)
        } catch {
            problem = Problem(error)
        }
    }
}
