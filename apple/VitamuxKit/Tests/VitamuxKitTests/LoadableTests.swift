import Testing
import VitamuxKit

struct LoadableTests {
    struct Failure: Error {}

    @Test func `captures the value`() async {
        let state = await Loadable { 42 }
        #expect(state == .loaded(42))
        #expect(state.value == 42)
    }

    @Test func `a problem passes through unchanged`() async {
        let problem = Problem(title: "Not found", detail: "No such metric.")
        let state: Loadable<Int> = await Loadable { throw problem }
        #expect(state == .failed(problem))
        #expect(state.value == nil)
    }

    @Test func `any other error becomes a problem`() async {
        let state: Loadable<Int> = await Loadable { throw Failure() }
        guard case .failed(let problem) = state else {
            Issue.record("expected a failure, got \(state)")
            return
        }
        #expect(problem.title == "Something went wrong")
    }
}
