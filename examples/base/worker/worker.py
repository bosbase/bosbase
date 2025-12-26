import requests

def fetch_and_process_data(url):
    response = requests.get(url)
    data = response.json()
    
    return data

if __name__ == "__main__":
    url = "https://worker1.aogobo.com/api/collections/keywords/records?page=1&perPage=10"
    result = fetch_and_process_data(url)
    print("Processed Data:", result)