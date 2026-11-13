import AVFoundation
import SwiftUI
import UIKit

/// 掃描 QR code 的畫面(相機;模擬器沒有相機時顯示說明)
struct QRScannerSheet: View {
    @Environment(\.dismiss) private var dismiss
    let onScan: (String) -> Void
    @State private var problem: String?

    var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()
                if let problem {
                    VStack(spacing: 12) {
                        Image(systemName: "camera.fill").font(.largeTitle)
                        Text(problem).multilineTextAlignment(.center)
                    }
                    .foregroundStyle(.white).padding()
                } else {
                    QRScannerView(onScan: onScan, onProblem: { problem = $0 }).ignoresSafeArea()
                    RoundedRectangle(cornerRadius: 16).stroke(.white.opacity(0.8), lineWidth: 3)
                        .frame(width: 240, height: 240)
                }
            }
            .navigationTitle("掃描配對 QR code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } } }
            .task { await checkPermission() }
        }
    }

    private func checkPermission() async {
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized: break
        case .notDetermined:
            if !(await AVCaptureDevice.requestAccess(for: .video)) { problem = "沒有相機權限。請到「設定 › omixor」打開相機,或改用手動輸入。" }
        default:
            problem = "沒有相機權限。請到「設定 › omixor」打開相機,或改用手動輸入。"
        }
    }
}

struct QRScannerView: UIViewControllerRepresentable {
    let onScan: (String) -> Void
    let onProblem: (String) -> Void

    func makeUIViewController(context: Context) -> ScannerController {
        let c = ScannerController()
        c.onScan = onScan
        c.onProblem = onProblem
        return c
    }

    func updateUIViewController(_ c: ScannerController, context: Context) {}

    final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
        var onScan: ((String) -> Void)?
        var onProblem: ((String) -> Void)?
        private let capture = AVCaptureSession()
        private var preview: AVCaptureVideoPreviewLayer?
        private var done = false

        override func viewDidLoad() {
            super.viewDidLoad()
            view.backgroundColor = .black
            guard let device = AVCaptureDevice.default(for: .video),
                  let input = try? AVCaptureDeviceInput(device: device), capture.canAddInput(input) else {
                DispatchQueue.main.async { self.onProblem?("這台裝置沒有可用的相機(模擬器沒有相機)。請改用手動輸入,或用 omixor://pair 連結。") }
                return
            }
            capture.addInput(input)
            let output = AVCaptureMetadataOutput()
            guard capture.canAddOutput(output) else { return }
            capture.addOutput(output)
            output.setMetadataObjectsDelegate(self, queue: .main)
            output.metadataObjectTypes = [.qr]
            let p = AVCaptureVideoPreviewLayer(session: capture)
            p.videoGravity = .resizeAspectFill
            view.layer.addSublayer(p)
            preview = p
            let s = capture
            DispatchQueue.global(qos: .userInitiated).async { s.startRunning() }
        }

        override func viewDidLayoutSubviews() {
            super.viewDidLayoutSubviews()
            preview?.frame = view.bounds
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            let s = capture
            DispatchQueue.global(qos: .userInitiated).async { if s.isRunning { s.stopRunning() } }
        }

        func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput objects: [AVMetadataObject], from connection: AVCaptureConnection) {
            guard !done, let o = objects.first as? AVMetadataMachineReadableCodeObject, let text = o.stringValue else { return }
            done = true
            UINotificationFeedbackGenerator().notificationOccurred(.success)
            onScan?(text)
        }
    }
}
