package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"context"
	"io/ioutil"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleCalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
)

// Variables globales
var db *sql.DB
var config *oauth2.Config
var client *http.Client
var calendarConfig *oauth2.Config
var gmailConfig *oauth2.Config

var defaultHeures = []string{"09:00", "11:00", "13:00", "15:00", "17:00", "19:00"}

// Fonction d'initialisation de la base de données
func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./reservations.db")
	if err != nil {
		log.Fatal(err)
	}

	query := `
        CREATE TABLE IF NOT EXISTS reservations (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            nom TEXT,
            prenom TEXT,
            email TEXT,
            telephone TEXT,
            type_session TEXT,
            date_session TEXT,
            heure_session TEXT,
            nb_personnes INTEGER,
            message TEXT,
			google_event_id TEXT
        );
    `
	if _, err := db.Exec(query); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Table 'reservations' created or exists already.")

	queryAvailableSlots := `
    CREATE TABLE IF NOT EXISTS available_slots (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        date_session TEXT NOT NULL,
        heure_session TEXT NOT NULL,
        is_available BOOLEAN DEFAULT 1
    );
`
	if _, err := db.Exec(queryAvailableSlots); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Table 'available_slots' created or exists already.")
}

func initOAuth() {
	// Charger les credentials pour Google Calendar
	calendarCredentials, err := ioutil.ReadFile("calendar_credentials.json")
	if err != nil {
		log.Fatalf("Impossible de lire le fichier de credentials Google Calendar : %v", err)
	}
	calendarConfig, err = google.ConfigFromJSON(calendarCredentials, googleCalendar.CalendarScope)
	if err != nil {
		log.Fatalf("Erreur de configuration Google Calendar : %v", err)
	}

	// Charger les credentials pour Gmail
	gmailCredentials, err := ioutil.ReadFile("gmail_credentials.json")
	if err != nil {
		log.Fatalf("Impossible de lire le fichier de credentials Gmail : %v", err)
	}
	gmailConfig, err = google.ConfigFromJSON(gmailCredentials, "https://www.googleapis.com/auth/gmail.send")
	if err != nil {
		log.Fatalf("Erreur de configuration Gmail : %v", err)
	}
}

// Fonction pour obtenir un client authentifié en utilisant le refresh token
func getClient(config *oauth2.Config, tokenFile string) *http.Client {
	tok, err := readToken(tokenFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokenFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// Validation simple du format d'email
func isEmailValid(email string) bool {
	re := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	return re.MatchString(email)
}

// Lire le token depuis le fichier
func readToken(filename string) (*oauth2.Token, error) {
	tokFile, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	err = json.Unmarshal(tokFile, &tok)
	return &tok, err
}

// Fonction pour obtenir un token OAuth 2.0 et un refresh token
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Visitez cette URL pour autoriser l'application :\n%v\n", authURL)

	var authCode string
	fmt.Print("Entrez le code d'autorisation : ")
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Erreur lors de la lecture du code d'autorisation : %v", err)
	}

	tok, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		log.Fatalf("Erreur lors de l'échange du code d'autorisation : %v", err)
	}
	return tok
}

// Sauvegarder le token (access token et refresh token) dans un fichier
func saveToken(filename string, token *oauth2.Token) {
	file, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Erreur lors de la création du fichier de token : %v", err)
	}
	defer file.Close()
	json.NewEncoder(file).Encode(token)
}

// Fonction pour ajouter l'événement au calendrier Google
func addEventToCalendar(nom, prenom, email, telephone, typeSession, dateSession, heureSession, message, nb_personnes string) (string, error) {
	// Créer un client authentifié pour Google Calendar
	client := getClient(calendarConfig, "calendar_token.json")
	srv, err := googleCalendar.New(client)
	if err != nil {
		log.Printf("Erreur : impossible de créer le client Google Calendar : %v", err)
		return "", fmt.Errorf("erreur technique : impossible de traiter votre demande")
	}

	// Convertir la date et l'heure de la session en un format compatible avec Google Calendar
	sessionTime := fmt.Sprintf("%sT%s:00", dateSession, heureSession)
	startTime, err := time.Parse("2006-01-02T15:04:05", sessionTime)
	if err != nil {
		log.Printf("Erreur : format de date ou d'heure invalide : %v", err)
		return "", fmt.Errorf("erreur technique : format de date ou d'heure invalide")
	}

	// Créer l'événement
	event := &googleCalendar.Event{
		Summary:     fmt.Sprintf("Réservation de %s - %s", nom, typeSession),
		Description: fmt.Sprintf("Téléphone: %s\nEmail: %s\nMessage: %s\nNombre de personnes: %s", telephone, email, message, nb_personnes),
		Start: &googleCalendar.EventDateTime{
			DateTime: startTime.Format(time.RFC3339),
			TimeZone: "Europe/Paris",
		},
		End: &googleCalendar.EventDateTime{
			DateTime: startTime.Add(2 * time.Hour).Format(time.RFC3339), // Durée de 2 heures
			TimeZone: "Europe/Paris",
		},
		Attendees: []*googleCalendar.EventAttendee{
			{Email: "damstrafic450@gmail.com"},
			{Email: email},
		},
		Reminders: &googleCalendar.EventReminders{
			UseDefault: true,
		},
	}

	// Ajouter l'événement au calendrier Google
	createdEvent, err := srv.Events.Insert("primary", event).Do()
	if err != nil {
		// Vérifier si l'erreur est liée à un token expiré
		if googleErr, ok := err.(*googleapi.Error); ok && googleErr.Code == 401 {
			log.Printf("Erreur : le token d'accès a expiré. Veuillez régénérer le token : %v", err)
			return "", fmt.Errorf("erreur technique : impossible de traiter votre demande pour le moment")
		}

		// Autres types d'erreurs
		log.Printf("Erreur lors de l'ajout de l'événement au calendrier : %v", err)
		return "", fmt.Errorf("erreur technique : impossible de traiter votre demande")
	}

	// Log de l'ID de l'événement généré pour le débogage
	log.Printf("ID de l'événement Google Calendar généré : %s", createdEvent.Id)

	// Insérer la réservation dans la base de données
	query := `
        INSERT INTO reservations (nom, prenom, email, telephone, type_session, date_session, heure_session, nb_personnes, message, google_event_id)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `
	_, err = db.Exec(query, nom, prenom, email, telephone, typeSession, dateSession, heureSession, nb_personnes, message, createdEvent.Id)
	if err != nil {
		log.Printf("Erreur : impossible d'insérer la réservation dans la base de données : %v", err)
		return "", fmt.Errorf("erreur technique : impossible de traiter votre demande")
	}

	return createdEvent.Id, nil
}

func main() {
	initDB()
	initOAuth()

	// Route handlers
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	fk := http.FileServer(http.Dir("./Image_republik_entertainment"))
	http.Handle("/Image_republik_entertainment/", http.StripPrefix("/Image_republik_entertainment", fk))
	http.HandleFunc("/", accueilHandler)
	http.HandleFunc("/reservation", reservationHandler)
	http.HandleFunc("/a-propos", AproposHandler)
	http.HandleFunc("/services", ServiceHandler)
	http.HandleFunc("/portfolio", portofolioHandler)
	http.HandleFunc("/contact", contactHandler)
	http.HandleFunc("/api/heures-disponibles", getHeuresDisponibles)
	http.HandleFunc("/confirmation", confirmationHandler)

	fmt.Println("Server running on: http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func accueilHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/accueil.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func reservationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Récupérer les informations de la réservation
		nom := r.FormValue("nom")
		prenom := r.FormValue("prenom")
		email := r.FormValue("email")
		telephone := r.FormValue("telephone")
		typeSession := r.FormValue("type_session")
		dateSession := r.FormValue("date_session")
		heureSession := r.FormValue("heure_session")
		nbPersonnes := r.FormValue("nb_personnes")
		message := r.FormValue("message")

		// Validation
		if nom == "" || prenom == "" || email == "" || telephone == "" || typeSession == "" || dateSession == "" || heureSession == "" {
			http.Error(w, "Veuillez remplir tous les champs obligatoires.", http.StatusBadRequest)
			return
		}
		if !isEmailValid(email) {
			http.Error(w, "Le format de l'email est invalide.", http.StatusBadRequest)
			return
		}

		// Ajouter l'événement au calendrier Google
		eventID, err := addEventToCalendar(nom, prenom, email, telephone, typeSession, dateSession, heureSession, message, nbPersonnes)
		if err != nil {
			http.Error(w, "Erreur lors de l'ajout de l'événement au calendrier : "+err.Error(), http.StatusInternalServerError)
			return
		} else {
			fmt.Println("Réservation réussie !")
		}

		// Envoyer un email de confirmation au client
		clientSubject := "Confirmation de votre réservation"
		clientBody := fmt.Sprintf(`
Bonjour %s,

Votre réservation pour une session de type '%s' a été enregistrée pour le %s à %s.
Nous avons également ajouté cet événement à votre Google Calendar.

Vous pouvez consulter et gérer les détails de votre réservation en suivant ce lien : 
https://calendar.google.com/calendar

Cordialement,
L'équipe de Republik Photo
`, prenom, typeSession, dateSession, heureSession)

		if err := sendEmailWithGmail(email, clientSubject, clientBody); err != nil {
			log.Printf("Erreur lors de l'envoi de l'email au client : %v", err)
		}

		// Envoyer un email de notification au propriétaire
		ownerEmail := "damstrafic450@gmail.com"
		ownerSubject := "Nouvelle réservation reçue"
		ownerBody := fmt.Sprintf(`
Bonjour,

Une nouvelle réservation a été effectuée :

Nom : %s %s
Email : %s
Téléphone : %s
Type de session : %s
Date : %s
Heure : %s
Nombre de personnes : %s
Message : %s

Consultez le Google Calendar pour plus de détails : 
%s

Cordialement,
Votre système de réservation
`, nom, prenom, email, telephone, typeSession, dateSession, heureSession, nbPersonnes, message, eventID)

		if err := sendEmailWithGmail(ownerEmail, ownerSubject, ownerBody); err != nil {
			log.Printf("Erreur lors de l'envoi de l'email au propriétaire : %v", err)
		}

		// Rediriger vers la page de confirmation
		http.Redirect(w, r, "/confirmation", http.StatusSeeOther)
	} else {
		tmpl, _ := template.ParseFiles("templates/accueil.html")
		tmpl.Execute(w, nil)
	}
}

func AproposHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/apropos.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func ServiceHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/services.html")
	if err != nil {
		log.Println("Error parsing services.html:", err) // Ajout du log ici
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		log.Println("Error executing template:", err) // Ajout du log ici
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func portofolioHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/portfolio.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func contactHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/contact.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Fonction pour récupérer les heures disponibles
func getHeuresDisponibles(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date") // Récupérer la date sélectionnée
	if date == "" {
		http.Error(w, "Date non fournie", http.StatusBadRequest)
		return
	}

	// Récupérer les heures réservées pour la date sélectionnée
	query := "SELECT heure_session FROM reservations WHERE date_session = ?"
	rows, err := db.Query(query, date)
	if err != nil {
		http.Error(w, "Erreur lors de la récupération des données", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Collecter les heures réservées
	var reservedHeures []string
	for rows.Next() {
		var heure string
		if err := rows.Scan(&heure); err != nil {
			http.Error(w, "Erreur lors de la lecture des données", http.StatusInternalServerError)
			return
		}
		reservedHeures = append(reservedHeures, heure)
	}

	// Calculer les heures disponibles
	availableHeures := filterAvailableHeures(defaultHeures, reservedHeures)

	// Retourner les heures disponibles au format JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(availableHeures)
}

// Fonction pour filtrer les heures disponibles
func filterAvailableHeures(defaultHeures, reservedHeures []string) []string {
	available := []string{}
	reservedSet := make(map[string]bool)
	for _, heure := range reservedHeures {
		reservedSet[heure] = true
	}
	for _, heure := range defaultHeures {
		if !reservedSet[heure] {
			available = append(available, heure)
		}
	}
	return available
}

func sendEmailWithGmail(to, subject, body string) error {
	// Crée le client Gmail
	client := getClient(gmailConfig, "gmail_token.json")
	service, err := gmail.New(client)
	if err != nil {
		return fmt.Errorf("Erreur lors de la création du service Gmail : %v", err)
	}

	// Encodage de l'objet du mail en Base64 avec UTF-8
	encodedSubject := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="

	// Construction du message avec les en-têtes MIME
	var message bytes.Buffer
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	message.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	message.WriteString(fmt.Sprintf("From: 'Republik photo' <damstrafic450@gmail.com>\r\n"))
	message.WriteString(fmt.Sprintf("To: %s\r\n", to))
	message.WriteString(fmt.Sprintf("Subject: %s\r\n", encodedSubject))
	message.WriteString("\r\n" + body)

	// Encodage du message en Base64
	raw := base64.URLEncoding.EncodeToString(message.Bytes())

	// Remplacement des caractères spéciaux pour Gmail
	raw = strings.ReplaceAll(raw, "+", "-")
	raw = strings.ReplaceAll(raw, "/", "_")

	// Envoi du message via l'API Gmail
	_, err = service.Users.Messages.Send("me", &gmail.Message{
		Raw: raw,
	}).Do()
	if err != nil {
		return fmt.Errorf("Erreur lors de l'envoi de l'email : %v", err)
	}

	return nil
}

func confirmationHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/confirmation.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
