import SwiftUI

// Set to true to enable seeded bugs. Build with:
//   swift build -Xswiftc -DBUGGY
// Or run clean:
//   swift run

#if BUGGY
let isBuggy = true
#else
let isBuggy = false
#endif

@main
struct UnitConverterApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
                .frame(minWidth: 500, minHeight: 400)
        }
        .windowStyle(.titleBar)
    }
}

// MARK: - Data Model

enum ConversionCategory: String, CaseIterable, Identifiable {
    case temperature = "Temperature"
    case length = "Length"
    case weight = "Weight"
    case volume = "Volume"

    var id: String { rawValue }

    var units: [String] {
        switch self {
        case .temperature: return ["Celsius", "Fahrenheit", "Kelvin"]
        case .length: return ["Meters", "Feet", "Inches", "Kilometers", "Miles"]
        case .weight: return ["Kilograms", "Pounds", "Ounces", "Grams"]
        case .volume: return ["Liters", "Gallons", "Cups", "Milliliters"]
        }
    }
}

struct ConversionRecord: Identifiable {
    let id = UUID()
    let category: String
    let fromUnit: String
    let toUnit: String
    let inputValue: Double
    let result: Double
    let timestamp: Date
}

// MARK: - Conversion Logic

func convert(_ value: Double, from: String, to: String, category: ConversionCategory) -> Double {
    if from == to { return value }

    switch category {
    case .temperature:
        return convertTemperature(value, from: from, to: to)
    case .length:
        return convertViaBase(value, from: from, to: to, toBase: lengthToMeters, fromBase: lengthFromMeters)
    case .weight:
        return convertViaBase(value, from: from, to: to, toBase: weightToKg, fromBase: weightFromKg)
    case .volume:
        return convertViaBase(value, from: from, to: to, toBase: volumeToLiters, fromBase: volumeFromLiters)
    }
}

func convertTemperature(_ value: Double, from: String, to: String) -> Double {
    // Convert to Celsius first
    var celsius: Double
    switch from {
    case "Fahrenheit": celsius = (value - 32) * 5.0 / 9.0
    case "Kelvin": celsius = value - 273.15
    default: celsius = value
    }

    if isBuggy {
        // MAC-BUG-1: Fahrenheit conversion uses wrong formula (adds instead of subtracts 32)
        switch to {
        case "Fahrenheit": return celsius * 9.0 / 5.0 + 23  // Wrong: +23 instead of +32
        case "Kelvin": return celsius + 273.15
        default: return celsius
        }
    }

    switch to {
    case "Fahrenheit": return celsius * 9.0 / 5.0 + 32
    case "Kelvin": return celsius + 273.15
    default: return celsius
    }
}

func convertViaBase(_ value: Double, from: String, to: String,
                    toBase: (String) -> Double, fromBase: (String) -> Double) -> Double {
    let base = value * toBase(from)
    return base * fromBase(to)
}

func lengthToMeters(_ unit: String) -> Double {
    switch unit {
    case "Feet": return 0.3048
    case "Inches": return 0.0254
    case "Kilometers": return 1000.0
    case "Miles":
        if isBuggy {
            // MAC-BUG-2: Miles conversion factor is wrong (uses 1000 instead of 1609.34)
            return 1000.0
        }
        return 1609.34
    default: return 1.0 // Meters
    }
}

func lengthFromMeters(_ unit: String) -> Double {
    return 1.0 / lengthToMeters(unit)
}

func weightToKg(_ unit: String) -> Double {
    switch unit {
    case "Pounds": return 0.453592
    case "Ounces": return 0.0283495
    case "Grams": return 0.001
    default: return 1.0
    }
}

func weightFromKg(_ unit: String) -> Double {
    return 1.0 / weightToKg(unit)
}

func volumeToLiters(_ unit: String) -> Double {
    switch unit {
    case "Gallons": return 3.78541
    case "Cups": return 0.236588
    case "Milliliters": return 0.001
    default: return 1.0
    }
}

func volumeFromLiters(_ unit: String) -> Double {
    return 1.0 / volumeToLiters(unit)
}

// MARK: - Views

struct ContentView: View {
    @State private var selectedCategory: ConversionCategory = .temperature
    @State private var fromUnit: String = "Celsius"
    @State private var toUnit: String = "Fahrenheit"
    @State private var inputText: String = ""
    @State private var result: Double?
    @State private var history: [ConversionRecord] = []
    @State private var showHistory = true

    var body: some View {
        HSplitView {
            // Left: Converter
            VStack(alignment: .leading, spacing: 16) {
                Text("Unit Converter\(isBuggy ? " (Buggy)" : "")")
                    .font(.title)
                    .fontWeight(.bold)

                // Category picker
                Picker("Category", selection: $selectedCategory) {
                    ForEach(ConversionCategory.allCases) { cat in
                        Text(cat.rawValue).tag(cat)
                    }
                }
                .pickerStyle(.segmented)
                .onChange(of: selectedCategory) { _, newValue in
                    fromUnit = newValue.units[0]
                    toUnit = newValue.units.count > 1 ? newValue.units[1] : newValue.units[0]
                    result = nil
                }

                // From/To unit pickers
                HStack {
                    VStack(alignment: .leading) {
                        Text("From").font(.caption).foregroundColor(.secondary)
                        Picker("From", selection: $fromUnit) {
                            ForEach(selectedCategory.units, id: \.self) { Text($0) }
                        }
                    }
                    VStack(alignment: .leading) {
                        Text("To").font(.caption).foregroundColor(.secondary)
                        Picker("To", selection: $toUnit) {
                            ForEach(selectedCategory.units, id: \.self) { Text($0) }
                        }
                    }
                    Button("Swap") {
                        let tmp = fromUnit
                        fromUnit = toUnit
                        toUnit = tmp
                        if let _ = result { performConversion() }
                    }
                }

                // Input
                HStack {
                    TextField("Enter value", text: $inputText)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 200)
                        .onSubmit { performConversion() }
                    Button("Convert") { performConversion() }
                        .keyboardShortcut(.return, modifiers: [])
                }

                // Result
                if let result = result {
                    let formatted = String(format: "%.4f", result)
                    Text("\(inputText) \(fromUnit) = \(formatted) \(toUnit)")
                        .font(.title2)
                        .fontWeight(.semibold)
                        .padding()
                        .background(Color.accentColor.opacity(0.1))
                        .cornerRadius(8)
                        .accessibilityIdentifier("conversion-result")
                }

                Spacer()
            }
            .padding()
            .frame(minWidth: 300)

            // Right: History
            if showHistory {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Text("History")
                            .font(.headline)
                        Spacer()
                        Button("Clear") {
                            if isBuggy {
                                // MAC-BUG-3: Clear history button doesn't clear the list
                                // (clears only the first item)
                                if !history.isEmpty {
                                    history.removeFirst()
                                }
                            } else {
                                history.removeAll()
                            }
                        }
                    }

                    if history.isEmpty {
                        Text("No conversions yet")
                            .foregroundColor(.secondary)
                            .frame(maxHeight: .infinity)
                    } else {
                        List(history) { record in
                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(String(format: "%.2f", record.inputValue)) \(record.fromUnit) → \(String(format: "%.4f", record.result)) \(record.toUnit)")
                                    .font(.system(.body, design: .monospaced))
                                Text(record.category)
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                            }
                            .padding(.vertical, 2)
                        }
                    }

                    Text("\(history.count) conversion\(history.count != 1 ? "s" : "")")
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .accessibilityIdentifier("history-count")
                }
                .padding()
                .frame(minWidth: 200, maxWidth: 300)
            }
        }
        .toolbar {
            ToolbarItem {
                Button(showHistory ? "Hide History" : "Show History") {
                    if isBuggy {
                        // MAC-BUG-4: Toggle history button doesn't work
                        return
                    }
                    showHistory.toggle()
                }
            }
        }
    }

    func performConversion() {
        guard let value = Double(inputText) else {
            result = nil
            return
        }
        let converted = convert(value, from: fromUnit, to: toUnit, category: selectedCategory)
        result = converted

        let record = ConversionRecord(
            category: selectedCategory.rawValue,
            fromUnit: fromUnit,
            toUnit: toUnit,
            inputValue: value,
            result: converted,
            timestamp: Date()
        )

        if isBuggy {
            // MAC-BUG-5: History is prepended but also duplicates entries
            history.insert(record, at: 0)
            history.insert(record, at: 0)
        } else {
            history.insert(record, at: 0)
        }
    }
}
