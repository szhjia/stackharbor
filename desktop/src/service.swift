import Foundation
import ServiceManagement

// Executed in the application process, so SMAppService resolves this signed
// bundle rather than a helper bundle. C ABI avoids a runtime swift invocation.
@_cdecl("stackharbor_service")
public func stackharborService(_ operation: Int32) -> UnsafeMutablePointer<CChar>? {
    let agent = SMAppService.agent(plistName: "com.xahacktech.stackharbor.engine.plist")
    let service = operation >= 10 ? SMAppService.mainApp : agent
    do {
        switch operation % 10 {
        case 1: if service.status == .notRegistered || service.status == .notFound { try service.register() }
        case 2: if service.status != .notRegistered && service.status != .notFound { try service.unregister() }
        default: break
        }
        let name: String
        switch service.status {
        case .notRegistered: name = "not_registered"
        case .enabled: name = "enabled"
        case .requiresApproval: name = "requires_approval"
        case .notFound: name = "not_found"
        @unknown default: name = "unknown"
        }
        return strdup("{\"status\":\"\(name)\"}")
    } catch {
        let object: [String: String] = ["error": error.localizedDescription]
        let data = try! JSONSerialization.data(withJSONObject: object)
        return strdup(String(decoding: data, as: UTF8.self))
    }
}
