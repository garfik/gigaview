#!/bin/bash
# Script to upload an image to Gigaview server

if [ $# -eq 0 ]; then
    echo "Usage: $0 <file_path>"
    echo "Example: $0 ../../../img.jpg"
    exit 1
fi

FILE_PATH="$1"

if [ ! -f "$FILE_PATH" ]; then
    echo "Error: File '$FILE_PATH' not found"
    exit 1
fi

if [[ "$FILE_PATH" != /* ]]; then
    FILE_PATH="$(cd "$(dirname "$FILE_PATH")" && pwd)/$(basename "$FILE_PATH")"
fi

ORIGINAL_FILENAME=$(basename "$FILE_PATH")

echo "File to upload: $FILE_PATH"
echo "Original filename: $ORIGINAL_FILENAME"
echo ""

read -p "Site URL (with http:// or https://): " SITE_URL
if [ -z "$SITE_URL" ]; then
    echo "Error: Site URL is required"
    exit 1
fi

SITE_URL="${SITE_URL%/}"

if [[ ! "$SITE_URL" =~ ^https?:// ]]; then
    echo "Error: URL must start with http:// or https://"
    exit 1
fi

read -p "Token (can be empty): " TOKEN
read -p "Copyright text (can be empty): " COPYRIGHT_TEXT
read -p "Copyright URL (can be empty): " COPYRIGHT_URL

echo ""
echo "Sending request..."

UPLOAD_URL="${SITE_URL}/api/upload"

CURL_CMD="curl -s -w '\nHTTP_CODE:%{http_code}\n' -X POST"

if [ -n "$TOKEN" ]; then
    CURL_CMD="$CURL_CMD -H 'Authorization: Bearer $TOKEN'"
fi

# Add multipart/form-data fields
# Explicitly set filename to ensure original name is preserved
CURL_CMD="$CURL_CMD -F \"file=@$FILE_PATH;filename=$ORIGINAL_FILENAME\""

if [ -n "$COPYRIGHT_TEXT" ]; then
    CURL_CMD="$CURL_CMD -F 'copyright_text=$COPYRIGHT_TEXT'"
fi

if [ -n "$COPYRIGHT_URL" ]; then
    CURL_CMD="$CURL_CMD -F 'copyright_link=$COPYRIGHT_URL'"
fi

CURL_CMD="$CURL_CMD '$UPLOAD_URL'"
RESPONSE=$(eval $CURL_CMD)

HTTP_CODE=$(echo "$RESPONSE" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)
BODY=$(echo "$RESPONSE" | sed '/HTTP_CODE:/d')

echo ""
echo "HTTP code: $HTTP_CODE"
echo "Server response:"
echo "$BODY" | jq . 2>/dev/null || echo "$BODY"

if [ "$HTTP_CODE" -ge 200 ] && [ "$HTTP_CODE" -lt 300 ]; then
    echo ""
    echo "✓ Upload successful!"
    exit 0
else
    echo ""
    echo "✗ Upload failed (HTTP $HTTP_CODE)"
    exit 1
fi

