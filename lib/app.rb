# frozen_string_literal: true

require "rack/cors"
require "roda"
require "date"

require "versions/v1"
require "fx_service"
require "summary_calculator"

class App < Roda
  use Rack::Cors do
    allow do
      origins "*"
      resource "*", headers: :any, methods: [:get, :options]
    end
  end

  opts[:root] = File.expand_path("..", __FILE__)
  plugin :static,
    {
      "/" => "root.json",
      "/favicon.ico" => "favicon.ico",
      "/robots.txt" => "robots.txt",
      "/v1/openapi.json" => "v1/openapi.json",
    },
    header_rules: [
      [:all, { "cache-control" => "public, max-age=900" }],
    ]
  plugin :json
  plugin :not_found do
    { message: "not found" }
  end

  route do |r|
    r.is("health") do
      { status: "ok", timestamp: Time.now.iso8601 }
    end

    r.is("summary") do
      start_date = r.params["start_date"]
      end_date = r.params["end_date"]
      from = r.params["from"] || "EUR"
      to = r.params["to"] || "USD"
      breakdown = r.params["breakdown"]

      unless start_date && end_date
        response.status = 400
        return { error: "start_date and end_date are required" }
      end

      begin
        Date.parse(start_date)
        Date.parse(end_date)
      rescue ArgumentError
        response.status = 400
        return { error: "Invalid date format. Use YYYY-MM-DD" }
      end

      fx_data = FXService.fetch_range(start_date, end_date, from:, to:)
      summary = SummaryCalculator.calculate(fx_data, breakdown:)

      summary
    end

    r.is("ui") do
      response["Content-Type"] = "text/html; charset=utf-8"
      render_ui
    end

    r.on("v1") do
      r.run(Versions::V1)
    end
  end

  private

  def render_ui
    <<~HTML
      <!DOCTYPE html>
      <html>
      <head>
        <title>FX Summary</title>
        <style>
          body {
            font-family: Arial, sans-serif;
            max-width: 1200px;
            margin: 0 auto;
            padding: 20px;
            background: #f5f5f5;
          }
          .container {
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
          }
          h1 {
            color: #333;
            margin-bottom: 30px;
          }
          .form-group {
            margin-bottom: 15px;
          }
          label {
            display: block;
            margin-bottom: 5px;
            font-weight: bold;
            color: #555;
          }
          input, select {
            width: 100%;
            padding: 8px;
            border: 1px solid #ddd;
            border-radius: 4px;
            font-size: 14px;
            box-sizing: border-box;
          }
          button {
            background: #007bff;
            color: white;
            padding: 10px 20px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 16px;
            margin-top: 10px;
          }
          button:hover {
            background: #0056b3;
          }
          .results {
            margin-top: 30px;
            display: none;
          }
          .results.show {
            display: block;
          }
          table {
            width: 100%;
            border-collapse: collapse;
            margin-top: 20px;
          }
          th, td {
            padding: 12px;
            text-align: left;
            border-bottom: 1px solid #ddd;
          }
          th {
            background: #f8f9fa;
            font-weight: bold;
          }
          .totals {
            background: #e9ecef;
            font-weight: bold;
          }
          .positive {
            color: #28a745;
          }
          .negative {
            color: #dc3545;
          }
          .error {
            color: #dc3545;
            padding: 10px;
            background: #f8d7da;
            border-radius: 4px;
            margin-top: 20px;
          }
        </style>
      </head>
      <body>
        <div class="container">
          <h1>FX Rate Summary</h1>
          <form id="fxForm">
            <div class="form-group">
              <label for="start_date">Start Date:</label>
              <input type="date" id="start_date" name="start_date" required>
            </div>
            <div class="form-group">
              <label for="end_date">End Date:</label>
              <input type="date" id="end_date" name="end_date" required>
            </div>
            <div class="form-group">
              <label for="from">From Currency:</label>
              <input type="text" id="from" name="from" value="EUR" maxlength="3">
            </div>
            <div class="form-group">
              <label for="to">To Currency:</label>
              <input type="text" id="to" name="to" value="USD" maxlength="3">
            </div>
            <div class="form-group">
              <label for="breakdown">Breakdown:</label>
              <select id="breakdown" name="breakdown">
                <option value="">None</option>
                <option value="day">Day</option>
              </select>
            </div>
            <button type="submit">Get Summary</button>
          </form>
          <div id="results" class="results"></div>
        </div>
        <script>
          document.getElementById('fxForm').addEventListener('submit', async (e) => {
            e.preventDefault();
            const formData = new FormData(e.target);
            const params = new URLSearchParams(formData);
            const resultsDiv = document.getElementById('results');
            resultsDiv.innerHTML = '<p>Loading...</p>';
            resultsDiv.classList.add('show');
            
            try {
              const response = await fetch('/summary?' + params.toString());
              const data = await response.json();
              
              if (!response.ok) {
                resultsDiv.innerHTML = '<div class="error">' + data.error + '</div>';
                return;
              }
              
              let html = '<h2>Summary</h2>';
              html += '<table><thead><tr><th>Metric</th><th>Value</th></tr></thead><tbody>';
              html += '<tr class="totals"><td>Start Rate</td><td>' + (data.totals.start_rate || 'N/A') + '</td></tr>';
              html += '<tr class="totals"><td>End Rate</td><td>' + (data.totals.end_rate || 'N/A') + '</td></tr>';
              html += '<tr class="totals"><td>Total % Change</td><td>' + 
                (data.totals.total_pct_change !== null ? 
                  '<span class="' + (data.totals.total_pct_change >= 0 ? 'positive' : 'negative') + '">' + 
                  data.totals.total_pct_change + '%</span>' : 'N/A') + '</td></tr>';
              html += '<tr class="totals"><td>Mean Rate</td><td>' + (data.totals.mean_rate || 'N/A') + '</td></tr>';
              html += '</tbody></table>';
              
              if (data.breakdown && data.breakdown.length > 0) {
                html += '<h3>Daily Breakdown</h3>';
                html += '<table><thead><tr><th>Date</th><th>Rate</th><th>% Change</th></tr></thead><tbody>';
                data.breakdown.forEach(day => {
                  const pctClass = day.pct_change !== null && day.pct_change >= 0 ? 'positive' : 'negative';
                  html += '<tr>';
                  html += '<td>' + day.date + '</td>';
                  html += '<td>' + day.rate + '</td>';
                  html += '<td>' + (day.pct_change !== null ? 
                    '<span class="' + pctClass + '">' + day.pct_change + '%</span>' : 'N/A') + '</td>';
                  html += '</tr>';
                });
                html += '</tbody></table>';
              }
              
              resultsDiv.innerHTML = html;
            } catch (error) {
              resultsDiv.innerHTML = '<div class="error">Error: ' + error.message + '</div>';
            }
          });
        </script>
      </body>
      </html>
    HTML
  end
end
