package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const dataDir = "/data"
const maxUploadSize = 50 << 20 // 50MB
const distTemplateDir = "/app/dist-template"

var (
	authUser = os.Getenv("AUTH_USER")
	authPass = os.Getenv("AUTH_PASS")
)

func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if authUser == "" {
		return true // No auth configured
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user != authUser || pass != authPass {
		w.Header().Set("WWW-Authenticate", `Basic realm="GSD Reader"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

type Project struct {
	Name    string
	Path    string
	ModTime time.Time
}

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>GSD Reader</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            max-width: 600px;
            margin: 2rem auto;
            padding: 0 1rem;
            background: #f5f5f5;
            color: #333;
        }
        h1 { 
            font-size: 1.5rem; 
            border-bottom: 2px solid #333;
            padding-bottom: 0.5rem;
        }
        .projects { list-style: none; padding: 0; }
        .project {
            background: white;
            margin: 0.5rem 0;
            padding: 1rem;
            border-radius: 4px;
            box-shadow: 0 1px 3px rgba(0,0,0,0.1);
        }
        .project a {
            color: #0066cc;
            text-decoration: none;
            font-weight: 500;
            font-size: 1.1rem;
        }
        .project a:hover { text-decoration: underline; }
        .project .meta {
            color: #666;
            font-size: 0.85rem;
            margin-top: 0.25rem;
        }
        .empty {
            color: #666;
            font-style: italic;
            padding: 2rem;
            text-align: center;
        }
    </style>
</head>
<body>
    <h1>GSD Reader</h1>
    {{if .Projects}}
    <ul class="projects">
        {{range .Projects}}
        <li class="project">
            <a href="/{{.Path}}/">{{.Name}}</a>
            <div class="meta">Updated: {{.ModTime.Format "2006-01-02 15:04"}}</div>
        </li>
        {{end}}
    </ul>
    {{else}}
    <p class="empty">No projects yet. Use <code>npx @luutuankiet/gsd-reader dump</code> to upload.</p>
    {{end}}
</body>
</html>
`))

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	http.HandleFunc("/", handler)

	log.Printf("GSD Reader server starting on :%s", port)
	log.Printf("Serving files from %s", dataDir)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	if !checkAuth(w, r) {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")

	if strings.HasPrefix(path, "upload/") {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleUpload(w, r, strings.TrimPrefix(path, "upload/"))
		return
	}

	if strings.HasPrefix(path, "upload-markdown/") {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleMarkdownUpload(w, r, strings.TrimPrefix(path, "upload-markdown/"))
		return
	}

	if path == "" || path == "index.html" {
		handleIndex(w, r)
		return
	}

	filePath := filepath.Join(dataDir, path)

	if !strings.HasPrefix(filepath.Clean(filePath), dataDir) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	info, err := os.Stat(filePath)
	if err == nil && info.IsDir() {
		indexPath := filepath.Join(filePath, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
	}

	http.ServeFile(w, r, filePath)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	projects, err := listProjects(dataDir, "")
	if err != nil {
		log.Printf("Error listing projects: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].ModTime.After(projects[j].ModTime)
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, map[string]interface{}{
		"Projects": projects,
	}); err != nil {
		log.Printf("Error rendering template: %v", err)
	}
}

func listProjects(base, prefix string) ([]Project, error) {
	var projects []Project

	currentDir := filepath.Join(base, prefix)
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		subPath := filepath.Join(prefix, entry.Name())
		fullPath := filepath.Join(base, subPath)

		indexPath := filepath.Join(fullPath, "index.html")
		if info, err := os.Stat(indexPath); err == nil {
			projects = append(projects, Project{
				Name:    strings.TrimSuffix(subPath, "/gsd-lite"),
				Path:    subPath,
				ModTime: info.ModTime(),
			})
		} else {
			subProjects, err := listProjects(base, subPath)
			if err == nil {
				projects = append(projects, subProjects...)
			}
		}
	}

	return projects, nil
}

func handleUpload(w http.ResponseWriter, r *http.Request, projectPath string) {
	log.Printf("Upload request: %s %s (Content-Length: %d)", r.Method, r.URL.Path, r.ContentLength)

	if projectPath == "" {
		io.Copy(io.Discard, r.Body)
		http.Error(w, "Project path required", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(projectPath)
	if strings.Contains(cleanPath, "..") {
		io.Copy(io.Discard, r.Body)
		http.Error(w, "Invalid project path", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	targetDir := filepath.Join(dataDir, cleanPath)

	if err := os.RemoveAll(targetDir); err != nil {
		log.Printf("Error removing existing directory: %v", err)
		io.Copy(io.Discard, r.Body)
		http.Error(w, "Failed to prepare upload directory", http.StatusInternalServerError)
		return
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Printf("Error creating directory: %v", err)
		io.Copy(io.Discard, r.Body)
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	var uploadReader io.Reader
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		file, _, err := r.FormFile("file")
		if err != nil {
			log.Printf("Error reading form file: %v", err)
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}
		defer file.Close()
		uploadReader = file
	} else {
		uploadReader = r.Body
	}

	gz, err := gzip.NewReader(uploadReader)
	if err != nil {
		log.Printf("Error creating gzip reader: %v", err)
		http.Error(w, "Invalid gzip data", http.StatusBadRequest)
		return
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	fileCount := 0

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error reading tar: %v", err)
			http.Error(w, "Invalid tar data", http.StatusBadRequest)
			return
		}

		targetPath := filepath.Join(targetDir, header.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), targetDir) {
			log.Printf("Skipping suspicious path: %s", header.Name)
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				log.Printf("Error creating directory %s: %v", targetPath, err)
				http.Error(w, "Failed to extract", http.StatusInternalServerError)
				return
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				log.Printf("Error creating parent directory: %v", err)
				http.Error(w, "Failed to extract", http.StatusInternalServerError)
				return
			}

			f, err := os.Create(targetPath)
			if err != nil {
				log.Printf("Error creating file %s: %v", targetPath, err)
				http.Error(w, "Failed to extract", http.StatusInternalServerError)
				return
			}

			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				log.Printf("Error writing file %s: %v", targetPath, err)
				http.Error(w, "Failed to extract", http.StatusInternalServerError)
				return
			}
			f.Close()
			fileCount++
		}
	}

	log.Printf("Upload complete: %d files to %s", fileCount, cleanPath)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Uploaded %d files to %s\n", fileCount, cleanPath)
}

type markdownUploadRequest struct {
	Work         string `json:"work"`
	Project      string `json:"project"`
	Architecture string `json:"architecture"`
	BasePath     string `json:"base_path"`
}

func handleMarkdownUpload(w http.ResponseWriter, r *http.Request, projectPath string) {
	log.Printf("Markdown upload request: %s %s (Content-Length: %d)", r.Method, r.URL.Path, r.ContentLength)

	if projectPath == "" {
		http.Error(w, "Project path required", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(projectPath)
	if strings.Contains(cleanPath, "..") {
		http.Error(w, "Invalid project path", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB

	var req markdownUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Work == "" {
		http.Error(w, "work field is required", http.StatusBadRequest)
		return
	}

	if req.BasePath == "" {
		req.BasePath = "gsd-lite"
	}

	templatePath := filepath.Join(distTemplateDir, "index.html")
	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		log.Printf("Error reading template: %v", err)
		http.Error(w, "Template not found \u2014 run a full upload first or check dist-template", http.StatusInternalServerError)
		return
	}

	workB64 := base64.StdEncoding.EncodeToString([]byte(req.Work))
	projectB64 := base64.StdEncoding.EncodeToString([]byte(req.Project))
	archB64 := base64.StdEncoding.EncodeToString([]byte(req.Architecture))

	injectScript := fmt.Sprintf(
		`<script>window.__WORKLOG_CONTENT_B64__="%s";window.__PROJECT_CONTENT_B64__="%s";window.__ARCHITECTURE_CONTENT_B64__="%s";window.__GSD_BASE_PATH__="%s";</script>`,
		workB64, projectB64, archB64, req.BasePath,
	)

	indexHtml := string(templateBytes)
	indexHtml = strings.Replace(indexHtml, "</head>", injectScript+"\n</head>", 1)
	indexHtml = strings.ReplaceAll(indexHtml, `href="/`, `href="./`)
	indexHtml = strings.ReplaceAll(indexHtml, `src="/`, `src="./`)

	targetDir := filepath.Join(dataDir, cleanPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Printf("Error creating directory: %v", err)
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	indexPath := filepath.Join(targetDir, "index.html")
	if err := os.WriteFile(indexPath, []byte(indexHtml), 0644); err != nil {
		log.Printf("Error writing index.html: %v", err)
		http.Error(w, "Failed to write index.html", http.StatusInternalServerError)
		return
	}

	srcAssets := filepath.Join(distTemplateDir, "assets")
	dstAssets := filepath.Join(targetDir, "assets")
	if err := syncAssets(srcAssets, dstAssets); err != nil {
		log.Printf("Error syncing assets: %v", err)
		http.Error(w, "Failed to sync assets", http.StatusInternalServerError)
		return
	}

	log.Printf("Markdown upload complete: %s (work: %d bytes, project: %d bytes, arch: %d bytes)",
		cleanPath, len(req.Work), len(req.Project), len(req.Architecture))
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Rendered %s\n", cleanPath)
}

func syncAssets(src, dst string) error {
	os.RemoveAll(dst)

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, 0644)
	})
}
