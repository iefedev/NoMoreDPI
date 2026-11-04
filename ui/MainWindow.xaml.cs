using System;
using System.Windows;
using Wpf.Ui.Appearance;
using Wpf.Ui.Controls;

namespace NoMoreDPI;

public partial class MainWindow : FluentWindow
{
    public MainWindow()
    {
        InitializeComponent();
        DataContext = AppState.Current;
        Icon = Icons.Image(Icons.Active);
        Loaded += (_, _) =>
        {
            SystemThemeWatcher.Watch(this);
            Nav.Navigate(typeof(Pages.StatusPage));
#if DEBUG
            if (Demo.StartPage != null) Nav.Navigate(Demo.StartPage);
#endif
        };
    }

    public void Reveal()
    {
        Show();
        if (WindowState == WindowState.Minimized) WindowState = WindowState.Normal;
        Activate();
        _ = AppState.Current.PollAsync();
    }

    public void NavigateTo(Type page) => Nav.Navigate(page);

    async void Save_Click(object sender, RoutedEventArgs e)
    {
        SaveButton.IsEnabled = false;
        try { await AppState.Current.SaveAsync(); }
        catch (Exception ex) { Dialogs.Error("Could not save: " + ex.Message); }
        SaveButton.IsEnabled = true;
    }

    async void Discard_Click(object sender, RoutedEventArgs e) => await AppState.Current.LoadAsync();
}
