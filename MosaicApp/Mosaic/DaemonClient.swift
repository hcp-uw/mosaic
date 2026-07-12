import Foundation

// Mirrors the Go ManifestEntry struct for decoding .mosaic-manifest.json in Swift.
struct ManifestEntry: Decodable {
  let name: String
  let size: Int
  let nodeID: Int
  let dateAdded: String
  let cached: Bool
}

/// Thin HTTP wrapper around the Go daemon's local API at localhost:7777.
/// All real logic lives in the daemon — this class is just URL plumbing.
class DaemonClient {
  static let shared = DaemonClient()
  private let base = URL(string: "http://localhost:7777")!

  // MARK: - File list

  struct FileStatus: Decodable {
    let name: String
    let size: Int
    let nodeID: Int
    let dateAdded: String
    let isCached: Bool
  }

  func listFiles(completion: @escaping ([FileStatus]) -> Void) {
    let url = base.appendingPathComponent("files")
    URLSession.shared.dataTask(with: url) { data, _, _ in
      guard let data, let files = try? JSONDecoder().decode([FileStatus].self, from: data) else {
        completion([])
        return
      }
      completion(files)
    }.resume()
  }

  // MARK: - File status (for badge)

  func getFileStatus(_ filename: String, completion: @escaping (FileStatus?) -> Void) {
    let url = base.appendingPathComponent("files/\(filename)/info")
    URLSession.shared.dataTask(with: url) { data, _, _ in
      guard let data, let status = try? JSONDecoder().decode(FileStatus.self, from: data) else {
        completion(nil)
        return
      }
      completion(status)
    }.resume()
  }

  // MARK: - Delete

  func delete(_ filename: String, completion: @escaping (Bool) -> Void) {
    var req = URLRequest(url: base.appendingPathComponent("files/\(filename)"))
    req.httpMethod = "DELETE"
    URLSession.shared.dataTask(with: req) { _, resp, _ in
      completion((resp as? HTTPURLResponse)?.statusCode == 200)
    }.resume()
  }

  // MARK: - Fetch (trigger download / cache locally)

  // Dedicated session for /fetch: the daemon blocks until the full file is
  // reconstructed and written, which can take minutes for large files.
  // URLSession.shared has a 60-second request timeout that fires mid-download.
  private static let fetchSession: URLSession = {
    let cfg = URLSessionConfiguration.default
    cfg.timeoutIntervalForRequest = 3600  // 1 hour — daemon controls real timeout
    return URLSession(configuration: cfg)
  }()

  func fetch(_ filename: String, completion: @escaping (Bool) -> Void) {
    var req = URLRequest(url: base.appendingPathComponent("files/\(filename)/fetch"))
    req.httpMethod = "POST"
    DaemonClient.fetchSession.dataTask(with: req) { _, resp, _ in
      completion((resp as? HTTPURLResponse)?.statusCode == 200)
    }.resume()
  }

  // MARK: - Rename

  func rename(_ filename: String, to newName: String, completion: @escaping (Bool) -> Void) {
    var req = URLRequest(url: base.appendingPathComponent("files/\(filename)/rename"))
    req.httpMethod = "POST"
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try? JSONSerialization.data(withJSONObject: ["newName": newName])
    URLSession.shared.dataTask(with: req) { _, resp, _ in
      completion((resp as? HTTPURLResponse)?.statusCode == 200)
    }.resume()
  }

  struct DownloadProgress: Decodable {
    let received: Int
    let needed: Int
  }

  func getDownloadProgress(completion: @escaping (DownloadProgress?) -> Void) {
    let url = base.appendingPathComponent("download-progress")
    URLSession.shared.dataTask(with: url) { data, _, _ in
      guard let data,
        let progress = try? JSONDecoder().decode(DownloadProgress.self, from: data)
      else { completion(nil); return }
      completion(progress)
    }.resume()
  }

  // MARK: - Upload progress

  struct UploadProgress: Decodable {
    let dispatched: Int
    let total: Int
  }

  func getUploadProgress(completion: @escaping (UploadProgress?) -> Void) {
    let url = base.appendingPathComponent("upload-progress")
    URLSession.shared.dataTask(with: url) { data, _, _ in
      guard let data,
        let progress = try? JSONDecoder().decode(UploadProgress.self, from: data)
      else { completion(nil); return }
      completion(progress)
    }.resume()
  }

  // MARK: - Active op

  struct ActiveOp: Decodable {
    let kind: String
    let description: String
  }

  func getActiveOp(completion: @escaping (ActiveOp?) -> Void) {
    let url = base.appendingPathComponent("active-op")
    URLSession.shared.dataTask(with: url) { data, _, _ in
      guard let data,
        let str = String(data: data, encoding: .utf8),
        str.trimmingCharacters(in: .whitespacesAndNewlines) != "null"
      else { completion(nil); return }
      completion(try? JSONDecoder().decode(ActiveOp.self, from: data))
    }.resume()
  }

  // MARK: - Network status

  struct PeerEntry: Decodable {
    let id: String
    let address: String
    let viaTURN: Bool
  }

  struct NetworkStatus: Decodable {
    let connected: Bool
    let peerCount: Int
    let peers: [PeerEntry]
  }

  func getNetworkStatus(completion: @escaping (NetworkStatus?) -> Void) {
    let url = base.appendingPathComponent("network-status")
    URLSession.shared.dataTask(with: url) { data, _, _ in
      guard let data,
        let status = try? JSONDecoder().decode(NetworkStatus.self, from: data)
      else { completion(nil); return }
      completion(status)
    }.resume()
  }

  // MARK: - Cancel

  func cancelOp(completion: @escaping () -> Void = {}) {
    var req = URLRequest(url: base.appendingPathComponent("cancel-op"))
    req.httpMethod = "POST"
    URLSession.shared.dataTask(with: req) { _, _, _ in completion() }.resume()
  }
}
