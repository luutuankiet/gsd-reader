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
	"regexp"
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

// Regex to extract base64 project content from index.html
var projectB64Re = regexp.MustCompile(`window\.__PROJECT_CONTENT_B64__\s*=\s*"([^"]*)"`)
var basePathRe = regexp.MustCompile(`window\.__GSD_BASE_PATH__\s*=\s*"([^"]*)"`)

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
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	FullPath    string    `json:"full_path"`
	Description string    `json:"description"`
	ModTime     time.Time `json:"mod_time"`
	ModTimeStr  string    `json:"mod_time_str"`
}

// extractProjectDescription reads index.html, finds __PROJECT_CONTENT_B64__,
// decodes it, and returns a meaningful project summary.
// Skips: title header (#), initialization stamp (*Initialized: ...*), section headers (##).
// Grabs: first real paragraph of content (typically under "## What This Is" or similar).
func extractProjectDescription(indexPath string) string {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}

	matches := projectB64Re.FindSubmatch(data)
	if len(matches) < 2 {
		return ""
	}

	decoded, err := base64.StdEncoding.DecodeString(string(matches[1]))
	if err != nil {
		return ""
	}

	content := string(decoded)
	lines := strings.Split(content, "\n")

	// Strategy: collect non-header, non-stamp content lines into paragraphs.
	// Return the first substantial paragraph (>20 chars).
	var currentPara []string
	var title string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Capture the h1 title for fallback
		if strings.HasPrefix(trimmed, "# ") && title == "" {
			title = strings.TrimPrefix(trimmed, "# ")
			continue
		}

		// Skip headers, init stamps, empty lines
		if strings.HasPrefix(trimmed, "#") {
			// If we already have a paragraph, check if it's good
			if len(currentPara) > 0 {
				result := strings.Join(currentPara, " ")
				if len(result) > 20 && !strings.HasPrefix(result, "Initialized:") {
					if len(result) > 500 {
						result = result[:500] + "..."
					}
					return result
				}
				currentPara = nil
			}
			continue
		}

		// Skip init stamp lines like *Initialized: 2026-01-31*
		if strings.HasPrefix(trimmed, "*Initialized:") || strings.HasPrefix(trimmed, "Initialized:") {
			continue
		}

		// Skip HTML comment placeholders (old template artifacts)
		if strings.HasPrefix(trimmed, "<!--") || strings.HasPrefix(trimmed, "[2-3 sentence") || strings.HasPrefix(trimmed, "[describe") {
			continue
		}

		// Blank line = paragraph break
		if trimmed == "" {
			if len(currentPara) > 0 {
				result := strings.Join(currentPara, " ")
				if len(result) > 20 && !strings.HasPrefix(result, "Initialized:") {
					if len(result) > 500 {
						result = result[:500] + "..."
					}
					return result
				}
				currentPara = nil
			}
			continue
		}

		// Strip leading markdown list markers and bold
		cleaned := trimmed
		if strings.HasPrefix(cleaned, "- ") {
			cleaned = strings.TrimPrefix(cleaned, "- ")
		}
		cleaned = strings.ReplaceAll(cleaned, "**", "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			currentPara = append(currentPara, cleaned)
		}
	}

	// Check last paragraph
	if len(currentPara) > 0 {
		result := strings.Join(currentPara, " ")
		if len(result) > 20 {
			if len(result) > 500 {
				result = result[:500] + "..."
			}
			return result
		}
	}

	// Fallback to title
	return title
}

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>GSD Reader</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            max-width: 900px;
            margin: 0 auto;
            padding: 1rem;
            background: #f5f5f5;
            color: #333;
        }
        h1 {
            font-size: 1.5rem;
            border-bottom: 2px solid #333;
            padding-bottom: 0.5rem;
            margin-bottom: 1rem;
        }

        /* Toolbar */
        .toolbar {
            display: flex;
            gap: 0.5rem;
            margin-bottom: 1rem;
            flex-wrap: wrap;
            align-items: center;
        }
        .search-box {
            flex: 1;
            min-width: 200px;
            padding: 0.6rem 0.8rem;
            border: 1px solid #ccc;
            border-radius: 6px;
            font-size: 0.95rem;
            outline: none;
            transition: border-color 0.2s;
        }
        .search-box:focus { border-color: #0066cc; box-shadow: 0 0 0 2px rgba(0,102,204,0.15); }
        .search-box::placeholder { color: #999; }

        .btn {
            padding: 0.6rem 1rem;
            border: 1px solid #ccc;
            border-radius: 6px;
            background: white;
            cursor: pointer;
            font-size: 0.85rem;
            transition: all 0.15s;
            white-space: nowrap;
        }
        .btn:hover { background: #f0f0f0; border-color: #999; }
        .btn-danger { color: #d32f2f; border-color: #d32f2f; }
        .btn-danger:hover { background: #fff0f0; }
        .btn-danger:disabled { opacity: 0.4; cursor: not-allowed; }
        .btn-select { font-size: 0.8rem; padding: 0.4rem 0.6rem; }

        .stats {
            font-size: 0.8rem;
            color: #666;
            margin-left: auto;
            white-space: nowrap;
        }

        /* Project List */
        .projects { list-style: none; }
        .project {
            background: white;
            margin: 0.4rem 0;
            padding: 0.8rem 1rem;
            border-radius: 6px;
            box-shadow: 0 1px 3px rgba(0,0,0,0.08);
            display: flex;
            align-items: flex-start;
            gap: 0.8rem;
            transition: box-shadow 0.15s;
        }
        .project:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.12); }
        .project.selected { background: #f0f7ff; border: 1px solid #b3d4fc; }

        .project input[type="checkbox"] {
            margin-top: 0.3rem;
            width: 16px;
            height: 16px;
            cursor: pointer;
            flex-shrink: 0;
        }

        .project-info { flex: 1; min-width: 0; }
        .project-header {
            display: flex;
            align-items: baseline;
            gap: 0.5rem;
            flex-wrap: wrap;
        }
        .project-name {
            color: #0066cc;
            text-decoration: none;
            font-weight: 600;
            font-size: 1.05rem;
        }
        .project-name:hover { text-decoration: underline; }
        .project-meta {
            font-size: 0.78rem;
            color: #888;
        }

        .project-path {
            font-size: 0.75rem;
            color: #999;
            margin-top: 0.2rem;
            font-family: "SF Mono", "Fira Code", monospace;
            display: flex;
            align-items: center;
            gap: 0.3rem;
        }
        .path-text {
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
            max-width: 500px;
            direction: rtl;
            text-align: left;
        }
        .path-text::before {
            content: "\200E"; /* LRM to fix RTL display */
        }
        .copy-path {
            cursor: pointer;
            opacity: 0.4;
            font-size: 0.7rem;
            padding: 0.1rem 0.3rem;
            border-radius: 3px;
            transition: opacity 0.15s;
            flex-shrink: 0;
        }
        .copy-path:hover { opacity: 1; background: #eee; }

        .project-desc {
            font-size: 0.82rem;
            color: #555;
            margin-top: 0.3rem;
            line-height: 1.4;
        }
        .project-desc .desc-text {
            display: -webkit-box;
            -webkit-line-clamp: 2;
            -webkit-box-orient: vertical;
            overflow: hidden;
        }
        .project-desc .desc-text.expanded {
            display: block;
            -webkit-line-clamp: unset;
        }
        .desc-toggle {
            color: #0066cc;
            cursor: pointer;
            font-size: 0.75rem;
            margin-top: 0.15rem;
            display: none;
        }
        .desc-toggle:hover { text-decoration: underline; }

        /* Pagination */
        .pagination {
            display: flex;
            justify-content: center;
            align-items: center;
            gap: 0.3rem;
            margin-top: 1rem;
            flex-wrap: wrap;
        }
        .page-btn {
            padding: 0.4rem 0.7rem;
            border: 1px solid #ddd;
            border-radius: 4px;
            background: white;
            cursor: pointer;
            font-size: 0.85rem;
            min-width: 2.2rem;
            text-align: center;
        }
        .page-btn:hover { background: #f0f0f0; }
        .page-btn.active { background: #0066cc; color: white; border-color: #0066cc; }
        .page-btn:disabled { opacity: 0.4; cursor: not-allowed; }

        .page-size-select {
            margin-left: 0.5rem;
            padding: 0.3rem;
            border: 1px solid #ddd;
            border-radius: 4px;
            font-size: 0.8rem;
        }

        .empty {
            color: #666;
            font-style: italic;
            padding: 2rem;
            text-align: center;
        }

        .no-results {
            text-align: center;
            padding: 2rem;
            color: #888;
        }

        /* Delete confirm modal */
        .modal-overlay {
            display: none;
            position: fixed;
            top: 0; left: 0; right: 0; bottom: 0;
            background: rgba(0,0,0,0.4);
            z-index: 1000;
            justify-content: center;
            align-items: center;
        }
        .modal-overlay.active { display: flex; }
        .modal {
            background: white;
            border-radius: 8px;
            padding: 1.5rem;
            max-width: 500px;
            width: 90%;
            box-shadow: 0 8px 32px rgba(0,0,0,0.2);
        }
        .modal h3 { margin-bottom: 1rem; color: #d32f2f; }
        .modal-list {
            max-height: 200px;
            overflow-y: auto;
            margin: 0.5rem 0 1rem 0;
            font-size: 0.85rem;
            background: #f9f9f9;
            padding: 0.5rem;
            border-radius: 4px;
        }
        .modal-list div { padding: 0.2rem 0; font-family: monospace; font-size: 0.8rem; }
        .modal-actions { display: flex; gap: 0.5rem; justify-content: flex-end; }

        /* Toast */
        .toast {
            position: fixed;
            bottom: 1rem;
            right: 1rem;
            background: #333;
            color: white;
            padding: 0.8rem 1.2rem;
            border-radius: 6px;
            font-size: 0.85rem;
            z-index: 2000;
            opacity: 0;
            transform: translateY(10px);
            transition: all 0.3s;
        }
        .toast.show { opacity: 1; transform: translateY(0); }

        @media (max-width: 600px) {
            body { padding: 0.5rem; }
            .path-text { max-width: 200px; }
            .project { padding: 0.6rem; }
            .toolbar { gap: 0.3rem; }
        }
    </style>
</head>
<body>
    <h1>📋 GSD Reader</h1>

    <div class="toolbar">
        <input type="text" class="search-box" id="search" placeholder="Search projects (name, description)..." autofocus>
        <button class="btn btn-select" id="selectAll" title="Select / deselect all visible">☐ All</button>
        <button class="btn btn-danger" id="deleteBtn" disabled>🗑 Delete (0)</button>
        <span class="stats" id="stats"></span>
    </div>

    <ul class="projects" id="projectList"></ul>
    <div class="no-results" id="noResults" style="display:none">No projects match your search.</div>

    <div class="pagination" id="pagination"></div>

    <!-- Delete confirmation modal -->
    <div class="modal-overlay" id="deleteModal">
        <div class="modal">
            <h3>⚠️ Delete Projects?</h3>
            <p>This will permanently remove these projects and all their data:</p>
            <div class="modal-list" id="deleteList"></div>
            <div class="modal-actions">
                <button class="btn" onclick="closeModal()">Cancel</button>
                <button class="btn btn-danger" id="confirmDelete">Yes, Delete</button>
            </div>
        </div>
    </div>

    <div class="toast" id="toast"></div>

    <script>
    // Server-injected project data
    const ALL_PROJECTS = {{.ProjectsJSON}};
    const PAGE_SIZES = [10, 15, 25, 50];
    let pageSize = 15;
    let currentPage = 1;
    let selectedPaths = new Set();
    let filteredProjects = [...ALL_PROJECTS];

    const $list = document.getElementById('projectList');
    const $search = document.getElementById('search');
    const $pagination = document.getElementById('pagination');
    const $stats = document.getElementById('stats');
    const $deleteBtn = document.getElementById('deleteBtn');
    const $selectAll = document.getElementById('selectAll');
    const $noResults = document.getElementById('noResults');
    const $deleteModal = document.getElementById('deleteModal');
    const $deleteList = document.getElementById('deleteList');
    const $toast = document.getElementById('toast');

    function render() {
        const start = (currentPage - 1) * pageSize;
        const page = filteredProjects.slice(start, start + pageSize);
        const totalPages = Math.ceil(filteredProjects.length / pageSize);

        // Stats
        const selCount = selectedPaths.size;
        $stats.textContent = filteredProjects.length + ' of ' + ALL_PROJECTS.length + ' projects';
        $deleteBtn.disabled = selCount === 0;
        $deleteBtn.textContent = '🗑 Delete (' + selCount + ')';

        // List
        if (page.length === 0) {
            $list.innerHTML = '';
            $noResults.style.display = filteredProjects.length === 0 && $search.value ? 'block' : 'none';
            if (ALL_PROJECTS.length === 0) {
                $list.innerHTML = '<li class="empty">No projects yet. Use <code>npx @luutuankiet/gsd-reader dump</code> to upload.</li>';
            }
        } else {
            $noResults.style.display = 'none';
            $list.innerHTML = page.map(function(p, i) {
                const checked = selectedPaths.has(p.path) ? 'checked' : '';
                const selClass = selectedPaths.has(p.path) ? ' selected' : '';
                const descHtml = p.description
                    ? '<div class="project-desc"><span class="desc-text" id="desc-' + i + '">' + escapeHtml(p.description) + '</span><span class="desc-toggle" id="toggle-' + i + '" onclick="toggleDesc(' + (start + i) + ')">▸ more</span></div>'
                    : '';
                return '<li class="project' + selClass + '">' +
                    '<input type="checkbox" ' + checked + ' data-path="' + escapeAttr(p.path) + '" onchange="toggleSelect(this)">' +
                    '<div class="project-info">' +
                        '<div class="project-header">' +
                            '<a class="project-name" href="/' + encodeURI(p.path) + '/">' + escapeHtml(p.name) + '</a>' +
                            '<span class="project-meta">' + p.mod_time_str + '</span>' +
                        '</div>' +
                        '<div class="project-path">' +
                            '<span class="path-text" title="' + escapeAttr(p.full_path) + '">' + escapeHtml(p.full_path) + '</span>' +
                            '<span class="copy-path" onclick="copyPath(\'' + escapeAttr(p.full_path) + '\')">📋</span>' +
                        '</div>' +
                        descHtml +
                    '</div>' +
                '</li>';
            }).join('');
        }

        // Pagination
        if (totalPages <= 1) {
            $pagination.innerHTML = '';
            requestAnimationFrame(checkDescOverflow);
            return;
        }

        let pHtml = '<button class="page-btn" onclick="goPage(' + (currentPage - 1) + ')"' +
            (currentPage === 1 ? ' disabled' : '') + '>&laquo;</button>';

        // Smart page range: show first, last, and neighbors
        const pages = buildPageRange(currentPage, totalPages);
        pages.forEach(function(pg) {
            if (pg === '...') {
                pHtml += '<span style="padding:0 0.3rem;color:#999">…</span>';
            } else {
                pHtml += '<button class="page-btn' + (pg === currentPage ? ' active' : '') +
                    '" onclick="goPage(' + pg + ')">' + pg + '</button>';
            }
        });

        pHtml += '<button class="page-btn" onclick="goPage(' + (currentPage + 1) + ')"' +
            (currentPage === totalPages ? ' disabled' : '') + '>&raquo;</button>';

        pHtml += '<select class="page-size-select" onchange="changePageSize(this.value)">';
        PAGE_SIZES.forEach(function(s) {
            pHtml += '<option value="' + s + '"' + (s === pageSize ? ' selected' : '') + '>' + s + '/page</option>';
        });
        pHtml += '</select>';

        $pagination.innerHTML = pHtml;

        // Check for description overflow after DOM update
        requestAnimationFrame(checkDescOverflow);
    }

    function buildPageRange(current, total) {
        if (total <= 7) {
            return Array.from({length: total}, function(_, i) { return i + 1; });
        }
        var pages = [];
        pages.push(1);
        if (current > 3) pages.push('...');
        for (var i = Math.max(2, current - 1); i <= Math.min(total - 1, current + 1); i++) {
            pages.push(i);
        }
        if (current < total - 2) pages.push('...');
        pages.push(total);
        return pages;
    }

    function doSearch() {
        var q = $search.value.toLowerCase().trim();
        if (!q) {
            filteredProjects = [...ALL_PROJECTS];
        } else {
            var terms = q.split(/\s+/);
            filteredProjects = ALL_PROJECTS.filter(function(p) {
                var haystack = (p.name + ' ' + p.description + ' ' + p.full_path).toLowerCase();
                return terms.every(function(t) { return haystack.indexOf(t) !== -1; });
            });
        }
        currentPage = 1;
        render();
    }

    function toggleSelect(el) {
        var path = el.dataset.path;
        if (el.checked) {
            selectedPaths.add(path);
        } else {
            selectedPaths.delete(path);
        }
        render();
    }

    $selectAll.addEventListener('click', function() {
        var start = (currentPage - 1) * pageSize;
        var page = filteredProjects.slice(start, start + pageSize);
        var allSelected = page.every(function(p) { return selectedPaths.has(p.path); });

        if (allSelected) {
            page.forEach(function(p) { selectedPaths.delete(p.path); });
        } else {
            page.forEach(function(p) { selectedPaths.add(p.path); });
        }
        render();
    });

    function goPage(p) {
        var totalPages = Math.ceil(filteredProjects.length / pageSize);
        if (p < 1 || p > totalPages) return;
        currentPage = p;
        render();
        window.scrollTo({top: 0, behavior: 'smooth'});
    }

    function changePageSize(val) {
        pageSize = parseInt(val);
        currentPage = 1;
        render();
    }

    function copyPath(path) {
        navigator.clipboard.writeText(path).then(function() {
            showToast('Path copied!');
        });
    }

    function showToast(msg) {
        $toast.textContent = msg;
        $toast.classList.add('show');
        setTimeout(function() { $toast.classList.remove('show'); }, 2000);
    }

    // Delete flow
    $deleteBtn.addEventListener('click', function() {
        if (selectedPaths.size === 0) return;
        $deleteList.innerHTML = Array.from(selectedPaths).map(function(p) {
            return '<div>' + escapeHtml(p) + '</div>';
        }).join('');
        $deleteModal.classList.add('active');
    });

    function closeModal() {
        $deleteModal.classList.remove('active');
    }

    document.getElementById('confirmDelete').addEventListener('click', function() {
        var paths = Array.from(selectedPaths);
        closeModal();
        $deleteBtn.disabled = true;
        $deleteBtn.textContent = '🗑 Deleting...';

        fetch('/api/projects/delete', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({paths: paths})
        })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                showToast('Error: ' + data.error);
                return;
            }
            // Remove deleted from ALL_PROJECTS
            var deletedSet = new Set(data.deleted || []);
            for (var i = ALL_PROJECTS.length - 1; i >= 0; i--) {
                if (deletedSet.has(ALL_PROJECTS[i].path)) {
                    ALL_PROJECTS.splice(i, 1);
                }
            }
            selectedPaths.clear();
            showToast('Deleted ' + deletedSet.size + ' project(s)');
            doSearch();
        })
        .catch(function(err) {
            showToast('Delete failed: ' + err.message);
            render();
        });
    });

    function toggleDesc(idx) {
        var descEl = document.getElementById('desc-' + idx);
        var toggleEl = document.getElementById('toggle-' + idx);
        if (!descEl || !toggleEl) return;
        if (descEl.classList.contains('expanded')) {
            descEl.classList.remove('expanded');
            toggleEl.textContent = '▸ more';
        } else {
            descEl.classList.add('expanded');
            toggleEl.textContent = '▾ less';
        }
    }

    // After rendering, show "more" toggle only for descriptions that overflow
    function checkDescOverflow() {
        document.querySelectorAll('.desc-text').forEach(function(el) {
            var toggleEl = el.nextElementSibling;
            if (!toggleEl) return;
            // Check if content is clamped (scrollHeight > clientHeight)
            if (el.scrollHeight > el.clientHeight + 2) {
                toggleEl.style.display = 'inline-block';
            } else {
                toggleEl.style.display = 'none';
            }
        });
    }

    // Escape helpers
    function escapeHtml(s) {
        var d = document.createElement('div');
        d.textContent = s;
        return d.innerHTML;
    }
    function escapeAttr(s) {
        return s.replace(/&/g,'&amp;').replace(/'/g,'&#39;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    }

    // Debounced search
    var searchTimer;
    $search.addEventListener('input', function() {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(doSearch, 200);
    });

    // Keyboard shortcut: Escape clears search
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') {
            if ($deleteModal.classList.contains('active')) {
                closeModal();
            } else if ($search.value) {
                $search.value = '';
                doSearch();
            }
        }
    });

    // Initial render
    render();
    </script>
</body>
</html>
`))

// extractBasePath reads the __GSD_BASE_PATH__ from a project's index.html.
// This is the origin path from the machine that uploaded the project,
// giving context about which server/directory the project lives on.
// Strips trailing /gsd-lite suffix and shows the last 2-3 meaningful path segments.
func extractBasePath(indexPath string) string {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}

	matches := basePathRe.FindSubmatch(data)
	if len(matches) < 2 {
		return ""
	}

	raw := string(matches[1])
	if raw == "" || raw == "gsd-lite" {
		return ""
	}

	// Strip trailing /gsd-lite
	raw = strings.TrimSuffix(raw, "/gsd-lite")

	// For short paths, show as-is
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	if len(parts) <= 4 {
		return raw
	}

	// Show first 2 segments (server hint: /home/ken, /Users/luutuankiet, /workspaces/EVERYTHING)
	// + last 2 segments (parent/project context)
	// e.g. /workspaces/EVERYTHING/joons/one-looker-extractor/beck-test/worktrees/feat__lookml_dashboard
	// becomes: /workspaces/EVERYTHING/…/worktrees/feat__lookml_dashboard
	return "/" + strings.Join(parts[:2], "/") + "/…/" + strings.Join(parts[len(parts)-2:], "/")
}

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

	// API routes
	if path == "api/projects" && r.Method == http.MethodGet {
		handleAPIProjects(w, r)
		return
	}
	if path == "api/projects/delete" && r.Method == http.MethodPost {
		handleAPIDelete(w, r)
		return
	}

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

	// Serialize to JSON for client-side rendering
	projectsJSON, err := json.Marshal(projects)
	if err != nil {
		log.Printf("Error marshaling projects: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, map[string]interface{}{
		"ProjectsJSON": template.JS(projectsJSON),
	}); err != nil {
		log.Printf("Error rendering template: %v", err)
	}
}

func handleAPIProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := listProjects(dataDir, "")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].ModTime.After(projects[j].ModTime)
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

type deleteRequest struct {
	Paths []string `json:"paths"`
}

type deleteResponse struct {
	Deleted []string `json:"deleted"`
	Errors  []string `json:"errors,omitempty"`
}

func handleAPIDelete(w http.ResponseWriter, r *http.Request) {
	var req deleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	resp := deleteResponse{}
	for _, p := range req.Paths {
		cleanPath := filepath.Clean(p)
		if strings.Contains(cleanPath, "..") {
			resp.Errors = append(resp.Errors, p+": invalid path")
			continue
		}

		fullPath := filepath.Join(dataDir, cleanPath)
		if !strings.HasPrefix(filepath.Clean(fullPath), dataDir) {
			resp.Errors = append(resp.Errors, p+": path traversal blocked")
			continue
		}

		if err := os.RemoveAll(fullPath); err != nil {
			resp.Errors = append(resp.Errors, p+": "+err.Error())
			continue
		}

		// Clean up empty parent directories
		parent := filepath.Dir(fullPath)
		for parent != dataDir {
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) > 0 {
				break
			}
			os.Remove(parent)
			parent = filepath.Dir(parent)
		}

		resp.Deleted = append(resp.Deleted, p)
		log.Printf("Deleted project: %s", cleanPath)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
			name := strings.TrimSuffix(subPath, "/gsd-lite")
			// Use origin base_path from upload for display (shows server/mount context)
			displayPath := extractBasePath(indexPath)
			if displayPath == "" {
				// Fallback: use the data-dir subPath without gsd-lite
				displayPath = strings.TrimSuffix(strings.ReplaceAll(subPath, string(filepath.Separator), "/"), "/gsd-lite")
			}

			projects = append(projects, Project{
				Name:        name,
				Path:        subPath,
				FullPath:    displayPath,
				Description: extractProjectDescription(indexPath),
				ModTime:     info.ModTime(),
				ModTimeStr:  info.ModTime().Format("2006-01-02 15:04"),
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
		http.Error(w, "Template not found — run a full upload first or check dist-template", http.StatusInternalServerError)
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