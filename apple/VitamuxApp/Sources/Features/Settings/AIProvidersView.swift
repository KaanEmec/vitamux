import Foundation
import Observation
import SwiftUI
import VitamuxKit

/// Settings › AI providers (the panel's `/settings/ai`): the `documents.external_ai.*.enabled`
/// toggles. Enabling a provider only makes it selectable; a lab PDF is sent to it only when the
/// owner consents for that document (docs/architecture/lab-documents.md).
@Observable
final class AIProvidersModel {
    struct Provider: Identifiable {
        var id: String
        var name: String
        var enabled: KeyPath<ServerSettings, Bool?>
        var patch: (Bool) -> ServerSettings
    }

    static let providers = [
        Provider(id: "gemini", name: "Google Gemini", enabled: \.documents_externalAi_gemini_enabled) {
            .init(documents_externalAi_gemini_enabled: $0)
        },
        Provider(id: "openai", name: "OpenAI", enabled: \.documents_externalAi_openai_enabled) {
            .init(documents_externalAi_openai_enabled: $0)
        },
        Provider(id: "openai_compatible", name: "OpenAI-compatible server", enabled: \.documents_externalAi_openaiCompatible_enabled) {
            .init(documents_externalAi_openaiCompatible_enabled: $0)
        },
    ]

    private(set) var settings: Loadable<ServerSettings> = .loading
    private(set) var saved = false
    private(set) var problem: Problem?

    func load(_ client: Client?) async {
        guard let client else { return }
        settings = await Loadable { try await client.getSettings().ok.body.json }
    }

    func isOn(_ provider: Provider) -> Bool {
        settings.value?[keyPath: provider.enabled] ?? false
    }

    func set(_ provider: Provider, on: Bool, client: Client?) async {
        guard let client else { return }
        saved = false
        problem = nil
        do {
            settings = .loaded(try await client.patch(provider.patch(on)))
            saved = true
        } catch {
            problem = Problem(error)
        }
    }
}

struct AIProvidersView: View {
    @Environment(AppState.self) private var state
    @State private var model = AIProvidersModel()

    var body: some View {
        List {
            Section {
                Text("Lab PDFs can be read by an external AI provider that extracts the printed rows for you to review. Nothing is sent until you consent for a specific document; enabling a provider here only lets you pick it.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            switch model.settings {
            case .loading:
                ProgressView().accessibilityLabel("Loading")
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                Section {
                    if model.saved { NoticeRow(text: "Saved.") }
                    if let problem = model.problem { ProblemRow(problem: problem) }
                    ForEach(AIProvidersModel.providers) { provider in
                        Toggle("Enable \(provider.name)", isOn: Binding {
                            model.isOn(provider)
                        } set: { on in
                            Task { await model.set(provider, on: on, client: state.client) }
                        })
                        .accessibilityIdentifier("ai-\(provider.id)")
                    }
                } header: {
                    Text("Providers for lab extraction")
                } footer: {
                    Text("When you consent for a document, its PDF is sent to the provider you pick. Each document asks for consent again, naming the provider and model; there is no standing consent.")
                }
            }
        }
        .navigationTitle("AI providers")
        .task { await model.load(state.client) }
    }
}
