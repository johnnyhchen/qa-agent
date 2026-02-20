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
struct ContactsApp: App {
    var body: some Scene {
        WindowGroup {
            NavigationStack {
                ContactListView()
            }
        }
    }
}

// MARK: - Data Model

struct Contact: Identifiable, Equatable {
    let id: UUID
    var firstName: String
    var lastName: String
    var email: String
    var phone: String
    var isFavorite: Bool

    var fullName: String {
        if isBuggy {
            // IOS-BUG-1: Full name shows last name first without comma
            return "\(lastName) \(firstName)"
        }
        return "\(firstName) \(lastName)"
    }
}

@Observable
class ContactStore {
    var contacts: [Contact] = []

    init() {
        // Seed with sample data
        contacts = [
            Contact(id: UUID(), firstName: "Alice", lastName: "Smith",
                    email: "alice@example.com", phone: "555-0101", isFavorite: true),
            Contact(id: UUID(), firstName: "Bob", lastName: "Johnson",
                    email: "bob@example.com", phone: "555-0102", isFavorite: false),
            Contact(id: UUID(), firstName: "Carol", lastName: "Williams",
                    email: "carol@example.com", phone: "555-0103", isFavorite: true),
            Contact(id: UUID(), firstName: "Dave", lastName: "Brown",
                    email: "dave@example.com", phone: "555-0104", isFavorite: false),
            Contact(id: UUID(), firstName: "Eve", lastName: "Davis",
                    email: "eve@example.com", phone: "555-0105", isFavorite: false),
        ]
    }

    func add(_ contact: Contact) {
        if isBuggy {
            // IOS-BUG-2: Add inserts at end instead of sorted position
            contacts.append(contact)
        } else {
            contacts.append(contact)
            contacts.sort { $0.lastName < $1.lastName }
        }
    }

    func delete(at offsets: IndexSet) {
        if isBuggy {
            // IOS-BUG-3: Delete removes wrong index (off by one)
            let adjusted = IndexSet(offsets.map { min($0 + 1, contacts.count - 1) })
            contacts.remove(atOffsets: adjusted)
        } else {
            contacts.remove(atOffsets: offsets)
        }
    }

    func toggleFavorite(_ contact: Contact) {
        guard let idx = contacts.firstIndex(where: { $0.id == contact.id }) else { return }
        contacts[idx].isFavorite.toggle()
    }

    func search(_ query: String) -> [Contact] {
        guard !query.isEmpty else { return contacts }
        let q = query.lowercased()
        if isBuggy {
            // IOS-BUG-4: Search only checks firstName, misses lastName and email
            return contacts.filter { $0.firstName.lowercased().contains(q) }
        }
        return contacts.filter {
            $0.firstName.lowercased().contains(q) ||
            $0.lastName.lowercased().contains(q) ||
            $0.email.lowercased().contains(q)
        }
    }

    var favoriteCount: Int {
        if isBuggy {
            // IOS-BUG-5: Favorite count is always 0
            return 0
        }
        return contacts.filter(\.isFavorite).count
    }
}

// MARK: - Views

struct ContactListView: View {
    @State private var store = ContactStore()
    @State private var searchText = ""
    @State private var showAddSheet = false

    var filteredContacts: [Contact] {
        store.search(searchText)
    }

    var body: some View {
        List {
            Section {
                HStack {
                    Label("\(store.contacts.count) contacts", systemImage: "person.2")
                    Spacer()
                    Label("\(store.favoriteCount) favorites", systemImage: "star.fill")
                        .foregroundColor(.yellow)
                }
                .font(.caption)
                .accessibilityIdentifier("contact-stats")
            }

            Section("All Contacts") {
                ForEach(filteredContacts) { contact in
                    NavigationLink(destination: ContactDetailView(contact: contact, store: store)) {
                        ContactRow(contact: contact)
                    }
                }
                .onDelete { offsets in
                    store.delete(at: offsets)
                }
            }
        }
        .searchable(text: $searchText, prompt: "Search contacts...")
        .navigationTitle("Contacts\(isBuggy ? " (Buggy)" : "")")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button(action: { showAddSheet = true }) {
                    Label("Add", systemImage: "plus")
                }
            }
        }
        .sheet(isPresented: $showAddSheet) {
            AddContactView(store: store)
        }
    }
}

struct ContactRow: View {
    let contact: Contact

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text(contact.fullName)
                    .font(.body)
                    .fontWeight(.medium)
                Text(contact.email)
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            Spacer()
            if contact.isFavorite {
                Image(systemName: "star.fill")
                    .foregroundColor(.yellow)
                    .font(.caption)
            }
        }
        .padding(.vertical, 2)
    }
}

struct ContactDetailView: View {
    let contact: Contact
    let store: ContactStore

    var body: some View {
        Form {
            Section("Name") {
                LabeledContent("First Name", value: contact.firstName)
                LabeledContent("Last Name", value: contact.lastName)
                LabeledContent("Full Name", value: contact.fullName)
                    .accessibilityIdentifier("full-name")
            }
            Section("Contact Info") {
                LabeledContent("Email", value: contact.email)
                LabeledContent("Phone", value: contact.phone)
            }
            Section {
                Button(contact.isFavorite ? "Remove from Favorites" : "Add to Favorites") {
                    store.toggleFavorite(contact)
                }
            }
        }
        .navigationTitle(contact.fullName)
    }
}

struct AddContactView: View {
    let store: ContactStore
    @Environment(\.dismiss) var dismiss
    @State private var firstName = ""
    @State private var lastName = ""
    @State private var email = ""
    @State private var phone = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Name") {
                    TextField("First Name", text: $firstName)
                    TextField("Last Name", text: $lastName)
                }
                Section("Contact Info") {
                    TextField("Email", text: $email)
                        .textContentType(.emailAddress)
                    TextField("Phone", text: $phone)
                        .textContentType(.telephoneNumber)
                }
            }
            .navigationTitle("New Contact")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        let contact = Contact(
                            id: UUID(),
                            firstName: firstName,
                            lastName: lastName,
                            email: email,
                            phone: phone,
                            isFavorite: false
                        )
                        store.add(contact)
                        dismiss()
                    }
                    .disabled(firstName.isEmpty && lastName.isEmpty)
                }
            }
        }
    }
}
