let map = null;
let currentImageId = null;
let tileLayer = null;
let drawnItems = null;
let uniqueTiles = new Set();
let downloadedBytes = 0;
let currentImageMeta = null;
let coordinateMarker = null;

function hideCoordinatesDisplay() {
  const coordInfo = document.getElementById("coordinates-info");
  if (coordInfo) {
    coordInfo.classList.add("hidden");
  }
}

function getBaseUrl() {
  // If BASE_URL wasn't replaced by the backend (still has placeholder),
  // use default localhost:8080 for local development
  if (!window.BASE_URL || window.BASE_URL === "__PUBLIC_BASE_URL__") {
    return "http://localhost:8080";
  }
  return window.BASE_URL;
}

async function loadImageList() {
  const listEl = document.getElementById("image-list");

  listEl.innerHTML =
    '<div class="text-gray-500 text-sm p-2 flex-shrink-0">Loading images...</div>';

  try {
    const response = await fetch(`${getBaseUrl()}/api/images`);
    const images = await response.json();

    if (!images.length) {
      listEl.innerHTML =
        '<div class="text-gray-500 text-sm p-2 flex-shrink-0">No images found</div>';
      return;
    }

    listEl.innerHTML = images
      .map(
        (img) => `
            <div class="p-2 border-r md:border-r-0 md:border-b cursor-pointer hover:bg-gray-100 flex-shrink-0 min-w-[150px] md:min-w-0" data-id="${img.id}">
                <div class="font-semibold text-xs md:text-sm truncate">${img.original_filename}</div>
                <div class="text-xs text-gray-500">${img.width} × ${img.height}</div>
            </div>
        `
      )
      .join("");
  } catch (error) {
    console.error("Failed to load images:", error);
    listEl.innerHTML =
      '<div class="text-red-500 text-sm p-2 flex-shrink-0">Failed to load images</div>';
  }
}

async function loadImage(imageId) {
  currentImageId = imageId;
  uniqueTiles.clear();
  downloadedBytes = 0;
  coordinateMarker = null;
  hideCoordinatesDisplay();
  updateDownloaded();

  try {
    // Remove existing map instance if present to avoid memory leaks
    if (map) {
      map.remove();
      map = null;
    }

    // Fetch image metadata (dimensions, zoom levels, tile size, etc.)
    const response = await fetch(`${getBaseUrl()}/api/images/${imageId}/meta`);
    currentImageMeta = await response.json();

    const fileSizeMB = (currentImageMeta.bytes / (1024 * 1024)).toFixed(2);
    document.getElementById("file-size").textContent = fileSizeMB;

    // Initialize Leaflet map with CRS.Simple coordinate system
    map = L.map("map", {
      // CRS.Simple treats the map as a flat plane with pixel coordinates
      // This is perfect for displaying large images as tile layers
      // because we don't geographic projection
      crs: L.CRS.Simple,
      minZoom: 0, // Minimum zoom level (full image view)
      maxZoom: currentImageMeta.maxZoom, // Maximum zoom level (highest detail)
      zoomSnap: 1, // Snap to integer zoom levels only
      zoomDelta: 1, // Zoom increment/decrement amount
      wheelPxPerZoom: 60, // Pixels to scroll per zoom level (smoother wheel zoom)
      inertia: true, // Enable momentum-based panning
    });

    // ----- Calculate image bounds in Leaflet's coordinate system
    // In CRS.Simple, we use unproject([x, y], zoom) to convert pixel coordinates
    // to Leaflet's internal lat/lng coordinates (which are just abstract numbers here)
    //
    // Important: In image coordinates, top-left = [0, 0], bottom-right = [width, height]
    // We need to convert these pixel coordinates to Leaflet's coordinate system
    // at the maximum zoom level to establish the image boundaries
    const northWest = map.unproject([0, 0], currentImageMeta.maxZoom);
    const southEast = map.unproject(
      [currentImageMeta.width, currentImageMeta.height],
      currentImageMeta.maxZoom
    );
    const bounds = L.latLngBounds(northWest, southEast);

    // Constrain map panning to image boundaries and set initial view
    // setMaxBounds prevents users from panning outside the image area
    map.setMaxBounds(bounds);
    // fitBounds automatically calculates center and zoom level to show entire image
    // padding: [0, 0] means no padding around the image edges
    map.fitBounds(bounds, { padding: [0, 0] });

    // ----- Create tile layer for the image
    // Tile URL pattern: z/x/y.jpeg where:
    //   z = zoom level (0 to maxZoom)
    //   x = tile column index
    //   y = tile row index
    tileLayer = L.tileLayer(
      `${getBaseUrl()}/api/images/${currentImageId}/tiles/{z}/{x}/{y}.jpeg`,
      {
        tileSize: currentImageMeta.tileSize, // Size of each tile in pixels
        minZoom: 0, // Minimum zoom level for tiles
        maxZoom: currentImageMeta.maxZoom, // Maximum zoom level for tiles
        noWrap: true, // Don't wrap tiles horizontally (prevent requests outside bounds)
        bounds, // Only request tiles within these bounds
        // Error tile: 1x1 transparent GIF shown when a tile fails to load (404, etc.)
        errorTileUrl:
          "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==",
        // Disable retina detection (we don't have @2x tiles)
        detectRetina: false,
      }
    ).addTo(map);

    // ----- Utility functions for coordinate conversion
    // These helpers convert between image pixel coordinates and Leaflet lat/lng coordinates
    // Useful for placing markers, overlays, or annotations at specific pixel positions

    function imagePointToLatLng(x, y) {
      return map.unproject([x, y], currentImageMeta.maxZoom);
    }
    function latLngToImagePoint(lat, lng) {
      const p = map.project([lat, lng], currentImageMeta.maxZoom);
      return { x: p.x, y: p.y };
    }

    // Create a feature group for drawn items (markers, shapes, etc.)
    // This can be used for annotations or user-drawn overlays on the image
    drawnItems = new L.FeatureGroup();
    map.addLayer(drawnItems);

    // ----- Single marker for coordinate display
    // Helper function to update coordinates in HUD
    function updateCoordinatesDisplay(latlng) {
      const imagePoint = latLngToImagePoint(latlng.lat, latlng.lng);
      const coordInfo = document.getElementById("coordinates-info");

      document.getElementById("coord-x").textContent = Math.round(imagePoint.x);
      document.getElementById("coord-y").textContent = Math.round(imagePoint.y);
      document.getElementById("coord-lat").textContent = latlng.lat.toFixed(6);
      document.getElementById("coord-lng").textContent = latlng.lng.toFixed(6);

      coordInfo.classList.remove("hidden");
    }

    // Handle map clicks to place or move the single marker
    map.on("click", (e) => {
      if (coordinateMarker) {
        // Move existing marker to new location
        coordinateMarker.setLatLng([e.latlng.lat, e.latlng.lng]);
        updateCoordinatesDisplay(e.latlng);
      } else {
        // Create new marker at click location
        coordinateMarker = L.marker([e.latlng.lat, e.latlng.lng], {
          draggable: false,
        });

        // Remove marker on click
        coordinateMarker.on("click", () => {
          drawnItems.removeLayer(coordinateMarker);
          coordinateMarker = null;
          hideCoordinatesDisplay();
        });

        coordinateMarker.addTo(drawnItems);
        updateCoordinatesDisplay(e.latlng);
      }
    });

    // ----- Add copyright control in bottom-right corner using Leaflet Control
    if (currentImageMeta.copyright_text || currentImageMeta.copyright_link) {
      const copyrightControl = L.control({ position: "bottomright" });

      copyrightControl.onAdd = function () {
        const div = L.DomUtil.create("div", "leaflet-control-copyright");
        div.classList.add(
          "bg-black",
          "bg-opacity-70",
          "px-3",
          "py-2",
          "rounded",
          "text-xs",
          "max-w-xs",
          "leading-relaxed",
          "[&_a]:text-blue-300",
          "[&_a]:underline",
          "[&_a:hover]:text-blue-200"
        );

        if (currentImageMeta.copyright_link) {
          const link = L.DomUtil.create("a", "", div);
          link.href = currentImageMeta.copyright_link;
          link.target = "_blank";
          link.rel = "noopener noreferrer";
          link.textContent =
            currentImageMeta.copyright_text || currentImageMeta.copyright_link;
        } else if (currentImageMeta.copyright_text) {
          div.textContent = currentImageMeta.copyright_text;
        }

        return div;
      };

      copyrightControl.addTo(map);
    }

    // ----- Track tile loading for download statistics
    // The 'tileload' event fires when a tile successfully loads
    // We use this to count unique tiles and calculate total bytes downloaded. Not really needed for the whole idea of the project, but this can showcase that in reality we are saving bandwidth by not downloading the whole image.
    tileLayer.on("tileload", (e) => {
      const url = e.tile.src;
      const match = url.match(/tiles\/(\d+)\/(\d+)\/(\d+)/);

      if (match) {
        const key = `${match[1]}/${match[2]}/${match[3]}`; // "z/x/y" format

        if (!uniqueTiles.has(key)) {
          uniqueTiles.add(key);

          fetch(url, { method: "HEAD", cache: "force-cache" })
            .then((res) => {
              if (!res.ok) {
                return;
              }

              const tileBytes = res.headers.get("X-Tile-Bytes");
              const contentLength = res.headers.get("Content-Length");
              const bytes = tileBytes
                ? parseInt(tileBytes, 10)
                : contentLength
                ? parseInt(contentLength, 10)
                : 0;

              if (bytes > 0) {
                downloadedBytes += bytes;
                updateDownloaded();
              }
            })
            .catch((err) => {
              console.debug(`Failed to get tile size for ${key}:`, err);
            });
        }
      }
    });
  } catch (error) {
    console.error("Failed to load image:", error);
    alert("Failed to load image: " + error.message);
  }
}

function updateDownloaded() {
  const mb = (downloadedBytes / (1024 * 1024)).toFixed(2);
  document.getElementById("downloaded").textContent = mb;
}

function pageView(pageUrl) {
  if (!pageUrl || typeof Image === "undefined") return;
  const img = new Image();
  const cacheBuster =
    Date.now().toString(36) + Math.random().toString(36).slice(2);
  img.src = "/hawk.png?u=" + encodeURIComponent(pageUrl) + "&_=" + cacheBuster;
}

// Handle logo click to show about page
const logoEl = document.getElementById("logo");
logoEl.addEventListener("click", () => {
  document.getElementById("about").classList.remove("hidden");
  document.getElementById("map").classList.add("hidden");
  downloadedBytes = 0;
  document.getElementById("file-size").textContent = "-";
  updateDownloaded();
  pageView("/");
});

const listEl = document.getElementById("image-list");
listEl.addEventListener("click", (e) => {
  const clickedElement = e.target.closest("[data-id]");
  if (clickedElement) {
    document.getElementById("about").classList.add("hidden");
    document.getElementById("map").classList.remove("hidden");
    loadImage(clickedElement.dataset.id);
    pageView(`/map/${clickedElement.dataset.id}`);
  }
});

// Handle window resize to update map size on mobile
window.addEventListener("resize", () => {
  if (map) {
    setTimeout(() => {
      map.invalidateSize();
    }, 100);
  }
});

loadImageList();
