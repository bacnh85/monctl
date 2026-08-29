// osd — tiny Swift helper that shows the NATIVE macOS brightness OSD
// (the same indicator macOS shows when brightness changes natively).
// Uses the com.apple.OSDUIHelper XPC service, same as MonitorControl and
// BetterDisplay.
//
// Usage: osd <displayID> <level 0..100>
// Built by `monctl autostart` into the monctl.app bundle.
import Foundation

private enum OSDImage: CLong { case brightness = 1 }

@objc private protocol OSDUIHelperProtocol {
    @objc(showImage:onDisplayID:priority:msecUntilFade:filledChiclets:totalChiclets:locked:)
    func showImage(
        _ img: CLong,
        onDisplayID displayID: UInt32,
        priority: CUnsignedInt,
        msecUntilFade: CUnsignedInt,
        filledChiclets: CUnsignedInt,
        totalChiclets: CUnsignedInt,
        locked: Bool
    )
}

let args = CommandLine.arguments
guard args.count >= 3,
      let displayID = UInt32(args[1]),
      let level = Double(args[2]), level >= 0, level <= 100 else {
    FileHandle.standardError.write("usage: osd <displayID> <level 0..100>\n".data(using: .utf8)!)
    exit(2)
}

let total = 16
let filled = CUnsignedInt((level / 100.0 * Double(total)).rounded())

let conn = NSXPCConnection(machServiceName: "com.apple.OSDUIHelper", options: [])
conn.remoteObjectInterface = NSXPCInterface(with: OSDUIHelperProtocol.self)
conn.resume()
guard let helper = conn.remoteObjectProxy as? OSDUIHelperProtocol else {
    FileHandle.standardError.write("osd: cannot reach com.apple.OSDUIHelper\n".data(using: .utf8)!)
    exit(1)
}
helper.showImage(
    OSDImage.brightness.rawValue,
    onDisplayID: displayID,
    priority: 0x1F4,
    msecUntilFade: 1200,
    filledChiclets: filled,
    totalChiclets: CUnsignedInt(total),
    locked: false
)
// Give the XPC message a beat to flush before the runloop-less process exits.
RunLoop.current.run(until: Date().addingTimeInterval(0.3))
