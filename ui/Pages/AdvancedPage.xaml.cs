using System.Windows.Controls;

namespace NoMoreDPI.Pages;

public partial class AdvancedPage : Page
{
    public AdvancedPage()
    {
        InitializeComponent();
        DataContext = AppState.Current;
    }
}
