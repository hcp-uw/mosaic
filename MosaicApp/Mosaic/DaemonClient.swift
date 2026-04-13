import Foundation

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

    func fetch(_ filename: String, completion: @escaping (Bool) -> Void) {
        var req = URLRequest(url: base.appendingPathComponent("files/\(filename)/fetch"))
        req.httpMethod = "POST"
        URLSession.shared.dataTask(with: req) { _, resp, _ in
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
}
