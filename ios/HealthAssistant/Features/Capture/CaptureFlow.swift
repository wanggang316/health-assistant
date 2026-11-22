import PhotosUI
import SwiftUI

/// Camera or library → recognizing → review → saved (§9.9). The camera slides
/// up as a full-screen cover.
struct CaptureFlow: View {
    var onDone: (MealEntryDTO?) -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    enum Stage { case pick, recognizing, review, saved }
    @State private var stage: Stage = .pick
    @State private var photo: Data?
    @State private var takenAt: Date = .now
    @State private var idempotencyKey = UUID().uuidString
    @State private var recognition: RecognitionDTO?
    @State private var draft: ReviewDraft?
    @State private var error: String?
    @State private var saving = false
    @State private var showCamera = false
    @State private var pickerItem: PhotosPickerItem?
    @State private var flash = false
    @State private var saved: MealEntryDTO?

    var body: some View {
        ZStack {
            switch stage {
            case .pick: pickView
            case .recognizing: RecognizingView(photo: photo, foodCount: recognition?.foodItems.count ?? 0, error: error,
                                               onRetry: { Task { await recognize() } }, onCancel: { onDone(nil) })
            case .review:
                if let draft {
                    ReviewView(draft: draft, photo: photo, confirmTitle: String(localized: "Confirm and log"), saving: saving, error: error,
                               onConfirm: { Task { await save() } }, onCancel: { onDone(nil) })
                    .transition(.move(edge: .bottom))
                }
            case .saved: SavedView(entry: saved) { onDone(saved) }
            }
            if flash { Color.white.ignoresSafeArea().transition(.opacity) }
        }
        .animation(.spring(duration: 0.45), value: stage)
        .fullScreenCover(isPresented: $showCamera) {
            CameraPicker { data in
                showCamera = false
                if let data { Task { await use(data, takenAt: .now) } }
            }
            .ignoresSafeArea()
        }
        .onChange(of: pickerItem) {
            guard let item = pickerItem else { return }
            Task {
                if let data = try? await item.loadTransferable(type: Data.self) {
                    await use(data, takenAt: PhotoCompressor.takenAt(from: data) ?? .now)
                }
            }
        }
    }

    private var pickView: some View {
        VStack(spacing: 24) {
            HStack {
                Button { onDone(nil) } label: { Image(systemName: "xmark").font(.system(size: 17, weight: .bold)) }
                    .foregroundStyle(Palette.ink2)
                Spacer()
                if let q = app.me?.recognitionQuota {
                    Text("\(q.remaining) recognitions left today").font(.system(size: 13)).foregroundStyle(Palette.ink3)
                }
            }
            .padding(.horizontal, 20)
            Spacer()
            Image("Scan").resizable().scaledToFit().frame(maxWidth: 240)
                .mask(RadialGradient(colors: [.black, .black, .clear], center: .center, startRadius: 70, endRadius: 125))
            Text("Photograph the whole meal from above, with the bowls and plates in view.")
                .font(.system(size: 16)).foregroundStyle(Palette.ink2).multilineTextAlignment(.center).padding(.horizontal, 32)
            Spacer()
            VStack(spacing: 12) {
                Button { showCamera = true } label: { Label("Take a photo", systemImage: "camera.fill") }
                    .buttonStyle(PrimaryButtonStyle())
                    .disabled(!UIImagePickerController.isSourceTypeAvailable(.camera))
                PhotosPicker(selection: $pickerItem, matching: .images) {
                    Label("Choose from library", systemImage: "photo.on.rectangle").frame(maxWidth: .infinity)
                }
                .buttonStyle(SecondaryButtonStyle())
                #if DEBUG
                if let sample = Bundle.main.url(forResource: "SampleMeal", withExtension: "jpg"), let data = try? Data(contentsOf: sample) {
                    Button("Use the sample photo") { Task { await use(data, takenAt: .now) } }
                        .font(.footnote).foregroundStyle(Palette.ink3)
                        .accessibilityIdentifier("useSamplePhoto")
                }
                #endif
            }
            .padding(.horizontal, 24).padding(.bottom, 24)
        }
        .background(Palette.rice)
    }

    private func use(_ raw: Data, takenAt: Date) async {
        guard let jpeg = await Task.detached(operation: { PhotoCompressor.jpeg(from: raw) }).value else {
            error = APIError.server(status: 400, code: "invalid_photo", message: "").userMessage
            return
        }
        if !reduceMotion {
            withAnimation(.easeOut(duration: 0.1)) { flash = true }
            withAnimation(.easeIn(duration: 0.35).delay(0.1)) { flash = false }
        }
        photo = jpeg
        self.takenAt = takenAt
        idempotencyKey = UUID().uuidString
        stage = .recognizing
        await recognize()
    }

    private func recognize() async {
        guard let photo else { return }
        error = nil
        do {
            let r = try await app.api.uploadRecognition(jpeg: photo, takenAt: takenAt,
                                                        offsetMinutes: TimeZone.current.secondsFromGMT(for: takenAt) / 60,
                                                        idempotencyKey: idempotencyKey)
            recognition = r
            if !r.isFood {
                error = String(localized: "No food found in this photo. Try another photo, or add the meal by hand.")
                return
            }
            try? await Task.sleep(for: .milliseconds(reduceMotion ? 0 : 380 * r.foodItems.count + 300))
            draft = ReviewDraft(recognition: r, takenAt: takenAt)
            stage = .review
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }

    private func save() async {
        guard let draft else { return }
        saving = true
        error = nil
        defer { saving = false }
        do {
            saved = try await app.api.createMealEntry(draft.input(), idempotencyKey: idempotencyKey)
            stage = .saved
        } catch let e as APIError {
            error = e.userMessage
        } catch {}
    }
}

/// The photo with a moving scan line; a pin pops in per dish found.
struct RecognizingView: View {
    var photo: Data?
    var foodCount: Int
    var error: String?
    var onRetry: () -> Void
    var onCancel: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var scan = false
    @State private var pins = 0

    var body: some View {
        VStack(spacing: 24) {
            Spacer()
            ZStack {
                if let photo, let img = UIImage(data: photo) {
                    Image(uiImage: img).resizable().scaledToFill()
                }
                if error == nil && !reduceMotion {
                    GeometryReader { g in
                        LinearGradient(colors: [.clear, Palette.yolk.opacity(0.9), .clear], startPoint: .top, endPoint: .bottom)
                            .frame(height: 60)
                            .offset(y: scan ? g.size.height - 30 : -30)
                            .animation(.easeInOut(duration: 1.4).repeatForever(autoreverses: true), value: scan)
                    }
                }
                ForEach(0..<pins, id: \.self) { i in
                    Circle().fill(Palette.tea).frame(width: 18, height: 18)
                        .overlay(Circle().stroke(.white, lineWidth: 3))
                        .offset(x: CGFloat([-60, 50, -20, 70, 0][i % 5]), y: CGFloat([-50, -20, 40, 60, 0][i % 5]))
                        .transition(.scale.combined(with: .opacity))
                }
            }
            .frame(width: 300, height: 300)
            .clipShape(RoundedRectangle(cornerRadius: 28, style: .continuous))
            .shadow(color: .black.opacity(0.12), radius: 20, y: 10)
            if let error {
                Text(error).font(.system(size: 15)).foregroundStyle(Palette.ink2).multilineTextAlignment(.center).padding(.horizontal, 32)
                Button("Try again", action: onRetry).buttonStyle(PrimaryButtonStyle()).padding(.horizontal, 48)
            } else {
                Text(foodCount > 0 ? String(localized: "Found \(foodCount) dishes") : String(localized: "Recognizing the dishes…"))
                    .font(.system(size: 17, weight: .semibold)).foregroundStyle(Palette.ink)
                    .contentTransition(.opacity)
                Text("Usually 5–15 seconds").font(.system(size: 13)).foregroundStyle(Palette.ink3)
            }
            Spacer()
            Button("Cancel", action: onCancel).foregroundStyle(Palette.ink3).padding(.bottom, 24)
        }
        .frame(maxWidth: .infinity)
        .background(Palette.rice)
        .onAppear { scan = true }
        .onChange(of: foodCount) {
            for i in 0..<foodCount {
                withAnimation(.spring(duration: 0.5, bounce: 0.3).delay(0.38 * Double(i))) { pins = i + 1 }
            }
        }
    }
}

/// Chips fly into a small bowl; then the flow closes.
struct SavedView: View {
    var entry: MealEntryDTO?
    var onClose: () -> Void
    @State private var landed = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(spacing: 20) {
            Spacer()
            ZStack {
                DayBowl(eaten: entry?.nutrients ?? .zero, targetKcal: (entry?.nutrients.kcal ?? 0) * 2.5, size: 180)
                ForEach(Array((entry?.foodItems ?? []).enumerated()), id: \.offset) { i, item in
                    Text(Names.pick(zh: item.nameZh, en: item.nameEn))
                        .font(.system(size: 13, weight: .semibold)).padding(.horizontal, 10).padding(.vertical, 6)
                        .background(Palette.card, in: Capsule()).foregroundStyle(Palette.ink)
                        .shadow(color: .black.opacity(0.1), radius: 4, y: 2)
                        .offset(x: landed ? 0 : CGFloat(i % 2 == 0 ? -110 : 110), y: landed ? 0 : CGFloat(-160 + i * 40))
                        .scaleEffect(landed ? 0.2 : 1)
                        .opacity(landed ? 0 : 1)
                        .animation(reduceMotion ? nil : .spring(duration: 0.7, bounce: 0.2).delay(0.12 * Double(i)), value: landed)
                }
            }
            Text("Logged").font(.system(size: 24, weight: .bold)).foregroundStyle(Palette.ink)
            Text("\(entry?.nutrients.kcal.kcalText ?? "0") kcal").font(.number(18)).foregroundStyle(Palette.ink2)
            Spacer()
        }
        .frame(maxWidth: .infinity)
        .background(Palette.rice)
        .onAppear {
            landed = true
            Task {
                try? await Task.sleep(for: .milliseconds(1400))
                onClose()
            }
        }
    }
}

/// UIImagePickerController camera wrapper.
struct CameraPicker: UIViewControllerRepresentable {
    var onFinish: (Data?) -> Void

    func makeUIViewController(context: Context) -> UIImagePickerController {
        let c = UIImagePickerController()
        c.sourceType = .camera
        c.delegate = context.coordinator
        return c
    }

    func updateUIViewController(_ uiViewController: UIImagePickerController, context: Context) {}
    func makeCoordinator() -> Coordinator { Coordinator(onFinish: onFinish) }

    final class Coordinator: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
        let onFinish: (Data?) -> Void
        init(onFinish: @escaping (Data?) -> Void) { self.onFinish = onFinish }

        func imagePickerController(_ picker: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
            let image = info[.originalImage] as? UIImage
            onFinish(image?.jpegData(compressionQuality: 0.95))
        }

        func imagePickerControllerDidCancel(_ picker: UIImagePickerController) { onFinish(nil) }
    }
}
