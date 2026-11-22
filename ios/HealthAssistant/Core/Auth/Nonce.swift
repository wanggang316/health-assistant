import CryptoKit
import Foundation

nonisolated enum NonceHash {
    /// sha256 of the server nonce, hex encoded; sent to Apple or Google (§3.1).
    static func sha256Hex(_ s: String) -> String {
        SHA256.hash(data: Data(s.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}
